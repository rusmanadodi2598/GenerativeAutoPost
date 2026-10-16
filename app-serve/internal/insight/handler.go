// Package insight serves the insight HTTP surface over one Service.
//
// @file      internal/insight/handler.go
// @for       Gin handlers for the video, stats, and profile routes.
// @uses      net/http, gin, internal/apierror
// @reason    Keeps request decoding, session resolution, and envelopes out of the service.
// @author    Dodi Rusmana <rusmanadodi@kentangtech.com>
// @layer     handler
// @stability stable
// @since     2026-10-12
package insight

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// QueryVideosRequest is the body of a video query.
type QueryVideosRequest struct {
	AccountID string   `json:"account_id" validate:"required"`
	VideoIDs  []string `json:"video_ids" validate:"required,min=1,max=20,dive,required"`
}

// Handler serves the insight HTTP surface.
type Handler struct {
	svc     *Service
	session SessionAccount
}

// NewHandler wires the insight handlers to the service and the session resolver.
func NewHandler(svc *Service, session SessionAccount) *Handler {
	return &Handler{svc: svc, session: session}
}

// List answers one page of the account's videos.
func (h *Handler) List(c *gin.Context) {
	operatorID, ok := operatorFrom(c)
	if !ok {
		return
	}
	maxCount, ok := maxCountQuery(c.Query("max_count"))
	if !ok {
		writeValidation(c, "Max count must be an integer between 1 and 20.")
		return
	}
	page, err := h.svc.ListVideos(c.Request.Context(), operatorID, c.Query("account_id"), c.Query("cursor"), maxCount)
	if err != nil {
		writeError(c, err)
		return
	}
	h.writeSuccess(c, http.StatusOK, page)
}

// Query answers the videos matching the requested ids.
func (h *Handler) Query(c *gin.Context) {
	operatorID, ok := operatorFrom(c)
	if !ok {
		return
	}
	var req QueryVideosRequest
	if !bind(c, &req) {
		return
	}
	videos, err := h.svc.QueryVideos(c.Request.Context(), operatorID, req.AccountID, req.VideoIDs)
	if err != nil {
		writeError(c, err)
		return
	}
	h.writeSuccess(c, http.StatusOK, videos)
}

// Show answers one video, refreshing its cover URL through a query.
func (h *Handler) Show(c *gin.Context) {
	operatorID, ok := operatorFrom(c)
	if !ok {
		return
	}
	video, err := h.svc.Video(c.Request.Context(), operatorID, c.Query("account_id"), c.Param("video_id"))
	if err != nil {
		writeError(c, err)
		return
	}
	h.writeSuccess(c, http.StatusOK, video)
}

// Stats answers the series for the requested window, defaulting to 30 days.
// A fresh latest point is refreshed first, so the series always opens at the
// newest known value.
func (h *Handler) Stats(c *gin.Context) {
	operatorID, ok := operatorFrom(c)
	if !ok {
		return
	}
	window, err := parseRange(c.Query("range"))
	if err != nil {
		writeValidation(c, "Stats range must be one of 7d, 30d, 90d.")
		return
	}
	series, err := h.svc.StatsSeries(c.Request.Context(), operatorID, c.Query("account_id"), c.Param("video_id"), window)
	if err != nil {
		writeError(c, err)
		return
	}
	h.writeSuccess(c, http.StatusOK, series)
}

// RefreshStats always fetches and appends a new point.
func (h *Handler) RefreshStats(c *gin.Context) {
	operatorID, ok := operatorFrom(c)
	if !ok {
		return
	}
	snap, err := h.svc.RefreshStats(c.Request.Context(), operatorID, c.Query("account_id"), c.Param("video_id"))
	if err != nil {
		writeError(c, err)
		return
	}
	h.writeSuccess(c, http.StatusAccepted, snap)
}

// Profile answers the creator profile and caches the postable creator record.
func (h *Handler) Profile(c *gin.Context) {
	operatorID, ok := operatorFrom(c)
	if !ok {
		return
	}
	profile, err := h.svc.Profile(c.Request.Context(), operatorID, c.Param("account_id"))
	if err != nil {
		writeError(c, err)
		return
	}
	h.writeSuccess(c, http.StatusOK, profile)
}

// CreatorInfo answers the creator profile and privacy options from the short
// cache, so the publish form does not pay an upstream call per open.
func (h *Handler) CreatorInfo(c *gin.Context) {
	operatorID, ok := operatorFrom(c)
	if !ok {
		return
	}
	info, err := h.svc.CreatorInfo(c.Request.Context(), operatorID, c.Param("account_id"))
	if err != nil {
		writeError(c, err)
		return
	}
	h.writeSuccess(c, http.StatusOK, info)
}
