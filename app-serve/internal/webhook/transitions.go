// Package webhook maps TikTok events onto publish state transitions.
//
// @file      internal/webhook/transitions.go
// @for       Event name to updater call mapping for the dispatcher.
// @uses      context
// @reason    Keeps the dispatcher's orchestration and its event mapping in separate files so neither nears the line limit.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     service
// @stability stable
// @since     2026-10-13
package webhook

import "context"

// transitionFor decodes the envelope and selects the updater call. The second
// return reports an acknowledged no-op, which covers both unknown event names
// and the Video Kit events the Content Posting flow never uses.
func (d *Dispatcher) transitionFor(env Envelope) (transition, bool, error) {
	switch env.Event {
	case EventVideoUploadFailed, EventVideoPublishDone:
		d.logger.Info("webhook event ignored, unused by content posting", "event", env.Event)
		return transition{}, true, nil
	case EventAuthRemoved:
		return d.authTransition(env)
	case EventComplete, EventPubliclyAvailable, EventFailed, EventInboxDelivered,
		EventNoLongerPublicly:
		return d.publishTransition(env)
	default:
		d.logger.Info("webhook event ignored, unknown type", "event", env.Event)
		return transition{}, true, nil
	}
}

// publishTransition maps a post.publish event to its updater call. The
// transition receives the transaction bound updater rather than the process
// wide one, so every call lands inside the commit transaction.
func (d *Dispatcher) publishTransition(env Envelope) (transition, bool, error) {
	var content PostPublishContent
	if err := decodedContent(env, &content); err != nil {
		return transition{}, false, err
	}
	t := transition{eventType: env.Event, publishID: content.PublishID, postID: content.PostID}
	switch env.Event {
	case EventComplete, EventPubliclyAvailable:
		t.apply = func(ctx context.Context, up StatusUpdater) error {
			return up.MarkPublishComplete(ctx, content.PublishID, content.PostID)
		}
	case EventFailed:
		t.apply = func(ctx context.Context, up StatusUpdater) error {
			return up.MarkPublishFailed(ctx, content.PublishID, content.Reason)
		}
	case EventInboxDelivered:
		t.apply = func(ctx context.Context, up StatusUpdater) error {
			return up.MarkInboxDelivered(ctx, content.PublishID)
		}
	default:
		t.apply = func(ctx context.Context, up StatusUpdater) error {
			return up.MarkUnpublished(ctx, content.PublishID, content.PostID)
		}
	}
	return t, false, nil
}

// authTransition builds the account side transition for a removed authorization.
// It takes no StatusUpdater: the account side deletes token material rather
// than moving a publish attempt, so it runs inside the same transaction
// without the publish updater.
func (d *Dispatcher) authTransition(env Envelope) (transition, bool, error) {
	var content AuthorizationRemovedContent
	if err := decodedContent(env, &content); err != nil {
		return transition{}, false, err
	}
	t := transition{eventType: env.Event}
	t.apply = func(ctx context.Context, _ StatusUpdater) error {
		if d.onAuth == nil {
			return nil
		}
		return d.onAuth(ctx, env.UserOpenID)
	}
	return t, false, nil
}
