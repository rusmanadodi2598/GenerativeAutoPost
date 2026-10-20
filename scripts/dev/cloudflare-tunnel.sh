#!/usr/bin/env bash
# Cloudflare Tunnel setup for GenerativeAutoPost local development.
#
# The Cloudflare API token is read from CF_API_TOKEN or
# ~/.config/genauto/cloudflare.token. The tunnel token is written to
# ~/.config/genauto/tunnel.env with mode 600 and is never printed.
#
# Usage:
#   scripts/dev/cloudflare-tunnel.sh verify
#   scripts/dev/cloudflare-tunnel.sh setup
#   scripts/dev/cloudflare-tunnel.sh run
#   scripts/dev/cloudflare-tunnel.sh status
#   scripts/dev/cloudflare-tunnel.sh teardown --yes
#
# Environment:
#   CF_API_TOKEN        scoped API token, required by verify/setup/status/teardown
#   CF_API_TOKEN_FILE   default ~/.config/genauto/cloudflare.token
#   CF_ACCOUNT_ID       optional, resolved from the token when empty
#   CF_ZONE_NAME        default kentang.ai.id
#   TUNNEL_NAME         default genauto-dev
#   TUNNEL_HOSTNAME     default app-post.kentang.ai.id
#   TUNNEL_ORIGIN       default http://localhost:8080
#   ACCESS_EMAIL        optional, enables the Zero Trust allow policy
#   ACCESS_TEAM_DOMAIN  optional, default kentangtech
#   FORCE=1             replace an existing DNS record that is not the tunnel

set -euo pipefail

readonly API="https://api.cloudflare.com/client/v4"
readonly CF_API_TOKEN_FILE="${CF_API_TOKEN_FILE:-$HOME/.config/genauto/cloudflare.token}"
readonly TUNNEL_ENV_FILE="${TUNNEL_ENV_FILE:-$HOME/.config/genauto/tunnel.env}"
readonly TUNNEL_NAME="${TUNNEL_NAME:-genauto-dev}"
readonly TUNNEL_HOSTNAME="${TUNNEL_HOSTNAME:-app-post.kentang.ai.id}"
readonly TUNNEL_ORIGIN="${TUNNEL_ORIGIN:-http://localhost:8080}"
readonly ZONE_NAME="${CF_ZONE_NAME:-kentang.ai.id}"
readonly ACCESS_EMAIL="${ACCESS_EMAIL:-}"
readonly ACCESS_TEAM_DOMAIN="${ACCESS_TEAM_DOMAIN:-kentangtech}"

API_RESPONSE=""

info() { printf '==> %s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

usage() {
	sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'
}

require_token() {
	if [ -z "${CF_API_TOKEN:-}" ] && [ ! -f "$CF_API_TOKEN_FILE" ]; then
		die "no API token: set CF_API_TOKEN or write $CF_API_TOKEN_FILE"
	fi
	command -v jq >/dev/null || die "jq is required"
}

cf_token() {
	if [ -n "${CF_API_TOKEN:-}" ]; then
		printf '%s' "$CF_API_TOKEN"
		return 0
	fi
	[ -f "$CF_API_TOKEN_FILE" ] || die "no API token: set CF_API_TOKEN or write $CF_API_TOKEN_FILE"
	tr -d '\r\n' <"$CF_API_TOKEN_FILE"
}

urlencode() {
	jq -rn --arg v "$1" '$v|@uri'
}

api_call() {
	local method="$1" path="$2" body="${3:-}"
	local -a args=(-sS -X "$method" "$API$path" -H "Authorization: Bearer $(cf_token)")
	if [ -n "$body" ]; then
		args+=(-H "Content-Type: application/json" --data "$body")
	fi
	if ! API_RESPONSE="$(curl "${args[@]}")"; then
		return 1
	fi
	[ "$(jq -r '.success // false' <<<"$API_RESPONSE" 2>/dev/null)" = "true" ]
}

api() {
	local method="$1" path="$2" body="${3:-}"
	if ! api_call "$method" "$path" "$body"; then
		jq -r '.errors[]? | "  \(.code): \(.message)"' <<<"${API_RESPONSE:-}" 2>/dev/null >&2 || true
		die "$method $path failed"
	fi
	printf '%s' "$API_RESPONSE"
}

resolve_account() {
	if [ -n "${CF_ACCOUNT_ID:-}" ]; then
		printf '%s' "$CF_ACCOUNT_ID"
		return 0
	fi
	local ids count
	ids="$(api GET "/accounts" | jq -r '.result[].id')"
	count="$(printf '%s\n' "$ids" | grep -c . || true)"
	if [ "$count" -gt 1 ]; then
		printf 'accounts visible to this token:\n%s\n' "$ids" >&2
		die "set CF_ACCOUNT_ID to pick one"
	fi
	[ "$count" -eq 1 ] || die "the token sees no account; set CF_ACCOUNT_ID"
	printf '%s' "$ids"
}

resolve_zone() {
	local acct="$1" id
	id="$(api GET "/zones?name=$(urlencode "$ZONE_NAME")&account.id=$acct" | jq -r '.result[0].id // empty')"
	[ -n "$id" ] || die "zone $ZONE_NAME not found in account $acct"
	printf '%s' "$id"
}

find_tunnel() {
	local acct="$1"
	api GET "/accounts/$acct/cfd_tunnel?name=$(urlencode "$TUNNEL_NAME")&is_deleted=false" \
		| jq -r '.result[0].id // empty'
}

ensure_tunnel() {
	local acct="$1" id
	id="$(find_tunnel "$acct")"
	if [ -n "$id" ]; then
		info "tunnel $TUNNEL_NAME exists: $id"
		printf '%s' "$id"
		return 0
	fi
	id="$(api POST "/accounts/$acct/cfd_tunnel" \
		"$(jq -cn --arg n "$TUNNEL_NAME" '{name:$n,config_src:"cloudflare"}')" \
		| jq -r '.result.id')"
	info "created tunnel $TUNNEL_NAME: $id"
	printf '%s' "$id"
}

configure_ingress() {
	local acct="$1" id="$2" body
	body="$(jq -cn --arg h "$TUNNEL_HOSTNAME" --arg o "$TUNNEL_ORIGIN" \
		'{config:{ingress:[{hostname:$h,service:$o},{service:"http_status:404"}]}}')"
	api PUT "/accounts/$acct/cfd_tunnel/$id/configurations" "$body" >/dev/null
	info "ingress: $TUNNEL_HOSTNAME -> $TUNNEL_ORIGIN"
}

backup_dns() {
	local dir="$HOME/.config/genauto" file
	mkdir -p "$dir"
	file="$dir/dns-$TUNNEL_HOSTNAME.json"
	printf '%s\n' "$1" >"$file"
	chmod 600 "$file"
	info "old DNS record saved to $file"
}

ensure_dns() {
	local zone="$1" id="$2" target="$2.cfargotunnel.com"
	local resp rid rtype rcontent body
	resp="$(api GET "/zones/$zone/dns_records?name=$(urlencode "$TUNNEL_HOSTNAME")")"
	rid="$(jq -r '.result[0].id // empty' <<<"$resp")"
	body="$(jq -cn --arg n "$TUNNEL_HOSTNAME" --arg c "$target" \
		'{type:"CNAME",name:$n,content:$c,proxied:true,ttl:1,comment:"GenerativeAutoPost dev tunnel"}')"
	if [ -z "$rid" ]; then
		api POST "/zones/$zone/dns_records" "$body" >/dev/null
		info "created CNAME $TUNNEL_HOSTNAME -> $target"
		return 0
	fi
	rtype="$(jq -r '.result[0].type' <<<"$resp")"
	rcontent="$(jq -r '.result[0].content' <<<"$resp")"
	if [ "$rtype" = "CNAME" ] && [ "$rcontent" = "$target" ]; then
		info "DNS already points at the tunnel"
		return 0
	fi
	backup_dns "$resp"
	if [ "${FORCE:-0}" != "1" ]; then
		die "$TUNNEL_HOSTNAME is $rtype $rcontent; set FORCE=1 to replace it (backup written)"
	fi
	api DELETE "/zones/$zone/dns_records/$rid" >/dev/null
	api POST "/zones/$zone/dns_records" "$body" >/dev/null
	info "replaced $rtype $rcontent with CNAME -> $target"
}

write_tunnel_env() {
	local acct="$1" id="$2" token dir
	token="$(api GET "/accounts/$acct/cfd_tunnel/$id/token" | jq -r '.result')"
	[ -n "$token" ] && [ "$token" != "null" ] || die "tunnel token is empty"
	dir="$(dirname "$TUNNEL_ENV_FILE")"
	mkdir -p "$dir"
	(
		umask 077
		printf 'TUNNEL_TOKEN=%s\n' "$token" >"$TUNNEL_ENV_FILE"
	)
	info "tunnel token written to $TUNNEL_ENV_FILE"
}

ensure_access_org() {
	local acct="$1" domain
	if api_call GET "/accounts/$acct/access/organizations"; then
		domain="$(jq -r '.result.auth_domain // empty' <<<"$API_RESPONSE")"
		if [ -n "$domain" ]; then
			info "Access team domain: $domain"
			return 0
		fi
	fi
	domain="$(api POST "/accounts/$acct/access/organizations" \
		"$(jq -cn --arg n "$ACCESS_TEAM_DOMAIN" '{name:$n,auth_domain:$n}')" \
		| jq -r '.result.auth_domain')"
	info "created Access organization: $domain"
}

ensure_access_app() {
	local acct="$1" domain="$2" decision="$3" email="$4"
	local existing include body
	existing="$(api GET "/accounts/$acct/access/apps?per_page=100" \
		| jq -r --arg d "$domain" '.result[] | select(.domain == $d) | .id' | head -n 1)"
	if [ -n "$existing" ]; then
		info "Access app exists: $domain ($existing)"
		return 0
	fi
	if [ "$decision" = "bypass" ]; then
		include='[{"everyone":{}}]'
	else
		include="$(jq -cn --arg e "$email" '[{email:{email:$e}}]')"
	fi
	body="$(jq -cn --arg n "genauto dev $domain" --arg d "$domain" \
		--arg dec "$decision" --argjson inc "$include" \
		'{name:$n,domain:$d,type:"self_hosted",session_duration:"24h",app_launcher_visible:false,policies:[{name:($dec+" policy"),decision:$dec,include:$inc}]}')"
	api POST "/accounts/$acct/access/apps" "$body" >/dev/null
	info "created Access app: $domain ($decision)"
}

ensure_access() {
	local acct="$1"
	if [ -z "$ACCESS_EMAIL" ]; then
		info "ACCESS_EMAIL empty, leaving the hostname public"
		return 0
	fi
	ensure_access_org "$acct"
	ensure_access_app "$acct" "$TUNNEL_HOSTNAME/webhooks/tiktok" bypass ""
	ensure_access_app "$acct" "$TUNNEL_HOSTNAME" allow "$ACCESS_EMAIL"
}

cmd_verify() {
	require_token
	local acct zone
	if api_call GET "/user/tokens/verify"; then
		printf 'token:   %s\n' "$(jq -r '.result.status' <<<"$API_RESPONSE")"
	else
		printf 'token:   user verify refused (account-owned token), checking account access instead\n'
	fi
	acct="$(resolve_account)"
	zone="$(resolve_zone "$acct")"
	printf 'account: %s\n' "$acct"
	printf 'zone:    %s (%s)\n' "$ZONE_NAME" "$zone"
	api GET "/accounts/$acct/cfd_tunnel?per_page=1" >/dev/null && printf 'tunnel:  ok\n'
	api GET "/zones/$zone/dns_records?per_page=1" >/dev/null && printf 'dns:     ok\n'
	if [ -n "$ACCESS_EMAIL" ]; then
		api GET "/accounts/$acct/access/apps?per_page=1" >/dev/null && printf 'access:  ok\n'
	fi
}

cmd_setup() {
	require_token
	local acct zone id
	acct="$(resolve_account)"
	zone="$(resolve_zone "$acct")"
	id="$(ensure_tunnel "$acct")"
	configure_ingress "$acct" "$id"
	ensure_dns "$zone" "$id"
	write_tunnel_env "$acct" "$id"
	ensure_access "$acct"
	printf '\nnext:\n  %s run\n' "$0"
}

cmd_run() {
	[ -f "$TUNNEL_ENV_FILE" ] || die "no tunnel env: run setup first"
	set -a
	# shellcheck disable=SC1090
	. "$TUNNEL_ENV_FILE"
	set +a
	exec cloudflared --no-autoupdate tunnel run
}

cmd_status() {
	require_token
	local acct id
	acct="$(resolve_account)"
	id="$(find_tunnel "$acct")"
	[ -n "$id" ] || die "tunnel $TUNNEL_NAME not found"
	api GET "/accounts/$acct/cfd_tunnel/$id" \
		| jq -r '"name:    \(.result.name)\nid:      \(.result.id)\nstatus:  \(.result.status)\ncreated: \(.result.created_at)"'
}

cmd_teardown() {
	require_token
	if [ "${1:-}" != "--yes" ]; then
		die "teardown deletes DNS, Access apps, and the tunnel; pass --yes"
	fi
	local acct zone id rid domain app_id
	acct="$(resolve_account)"
	zone="$(resolve_zone "$acct")"
	id="$(find_tunnel "$acct")"
	if [ -z "$id" ]; then
		info "tunnel $TUNNEL_NAME not found, nothing to delete"
		return 0
	fi
	rid="$(api GET "/zones/$zone/dns_records?name=$(urlencode "$TUNNEL_HOSTNAME")" \
		| jq -r --arg c "$id.cfargotunnel.com" '.result[] | select(.content == $c) | .id' | head -n 1)"
	if [ -n "$rid" ]; then
		api DELETE "/zones/$zone/dns_records/$rid" >/dev/null
		info "deleted DNS record $TUNNEL_HOSTNAME"
	fi
	for domain in "$TUNNEL_HOSTNAME" "$TUNNEL_HOSTNAME/webhooks/tiktok"; do
		app_id="$(api GET "/accounts/$acct/access/apps?per_page=100" \
			| jq -r --arg d "$domain" '.result[] | select(.domain == $d) | .id' | head -n 1)"
		if [ -n "$app_id" ]; then
			api DELETE "/accounts/$acct/access/apps/$app_id" >/dev/null
			info "deleted Access app $domain"
		fi
	done
	api DELETE "/accounts/$acct/cfd_tunnel/$id/connections" >/dev/null
	api DELETE "/accounts/$acct/cfd_tunnel/$id" >/dev/null
	info "deleted tunnel $TUNNEL_NAME"
	rm -f "$TUNNEL_ENV_FILE"
}

case "${1:-}" in
verify) cmd_verify ;;
setup) cmd_setup ;;
run) cmd_run ;;
status) cmd_status ;;
teardown)
	shift
	cmd_teardown "${1:-}"
	;;
help | -h | --help) usage ;;
*)
	usage >&2
	exit 2
	;;
esac
