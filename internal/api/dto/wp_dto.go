package dto

import wpsvc "merger/backend/internal/services/wordpress"

type WPRegisterRequest struct {
	SiteURL string `json:"site_url" binding:"required"`
	APIKey  string `json:"api_key"  binding:"required"`
}

type WPRegisterResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"` // seconds
}

type WPRefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type WPRefreshResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

type WPSyncRequest struct {
	Users []wpsvc.WPUser `json:"users" binding:"required"`
}

type WPSyncResponse struct {
	Ingested int    `json:"ingested"`
	JobID    string `json:"job_id"`
}
