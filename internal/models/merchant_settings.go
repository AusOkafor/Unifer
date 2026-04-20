package models

import (
	"time"

	"github.com/google/uuid"
)

type MerchantSettings struct {
	// ── Billing ──────────────────────────────────────────────────────────────
	Plan                   string     `db:"plan"`                    // free | basic | pro
	ShopifySubscriptionID  *string    `db:"shopify_subscription_id"` // GID from appSubscriptionCreate
	MergesThisMonth        int        `db:"merges_this_month"`
	MergesMonthStart       time.Time  `db:"merges_month_start"`
	MerchantID           uuid.UUID `db:"merchant_id"`
	AutoDetect           bool      `db:"auto_detect"`
	ConfidenceThreshold  int       `db:"confidence_threshold"`
	RetentionDays        int       `db:"retention_days"`
	NotificationsEnabled bool      `db:"notifications_enabled"`

	// Detection
	ScanFrequency string `db:"scan_frequency"` // webhook | daily | manual
	ScanHour      int    `db:"scan_hour"`       // 0–23 UTC, only used when scan_frequency = "daily"
	SignalEmail   bool   `db:"signal_email"`
	SignalPhone   bool   `db:"signal_phone"`
	SignalAddress bool   `db:"signal_address"`
	SignalName    bool   `db:"signal_name"`

	// Risk & Safety
	RiskPolicy            string `db:"risk_policy"` // safe_only | allow_review | block_risky
	RequireAnchor         bool   `db:"require_anchor"`
	WeakLinkProtection    bool   `db:"weak_link_protection"`
	BlockDifferentCountry bool   `db:"block_different_country"`
	BlockFraudTags        bool   `db:"block_fraud_tags"`
	BlockDisabledAccounts bool   `db:"block_disabled_accounts"`

	// Bulk Merge
	BulkMaxBatch        int  `db:"bulk_max_batch"`
	BulkDelayMs         int  `db:"bulk_delay_ms"`
	BulkRequirePreview  bool `db:"bulk_require_preview"`

	// Granular notifications
	NotifyNewDuplicates bool `db:"notify_new_duplicates"`
	NotifyHighRisk      bool `db:"notify_high_risk"`
	NotifyBulkComplete  bool `db:"notify_bulk_complete"`
	NotifyFailures      bool `db:"notify_failures"`

	// Developer
	DebugMode bool `db:"debug_mode"`

	// Behavioral signals
	EnableBehavioralSignals bool `db:"enable_behavioral_signals"`
}

func DefaultSettings(merchantID uuid.UUID) *MerchantSettings {
	return &MerchantSettings{
		MerchantID:            merchantID,
		Plan:                  "free",
		MergesThisMonth:       0,
		MergesMonthStart:      time.Now().UTC(),
		AutoDetect:            true,
		ConfidenceThreshold:   75,
		RetentionDays:         90,
		NotificationsEnabled:  true,
		ScanFrequency:         "webhook",
		ScanHour:              3,
		SignalEmail:           true,
		SignalPhone:           true,
		SignalAddress:         true,
		SignalName:            true,
		RiskPolicy:            "safe_only",
		RequireAnchor:         true,
		WeakLinkProtection:    true,
		BlockDifferentCountry: true,
		BlockFraudTags:        true,
		BlockDisabledAccounts: true,
		BulkMaxBatch:          25,
		BulkDelayMs:           500,
		BulkRequirePreview:    true,
		NotifyNewDuplicates:   true,
		NotifyHighRisk:        true,
		NotifyBulkComplete:    true,
		NotifyFailures:        true,
		DebugMode:             false,
	}
}
