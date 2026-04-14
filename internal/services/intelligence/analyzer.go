// Package intelligence provides pre-merge analysis for duplicate customer groups.
// All analysis runs from customer_cache data — no Shopify API calls required.
package intelligence

import (
	"encoding/json"
	"fmt"
	"time"

	"merger/backend/internal/models"
)

// FieldBreakdown holds per-field similarity scores (0–1) for the top-scoring
// pair in the cluster. Stored in intelligence_json and surfaced in the API
// so the frontend can render a real confidence breakdown chart.
type FieldBreakdown struct {
	EmailScore   float64      `json:"email_score"`
	NameScore    float64      `json:"name_score"`
	PhoneScore   float64      `json:"phone_score"`
	AddressScore float64      `json:"address_score"`
	// Reasons are prioritized human-readable explanations derived from the
	// component scores, e.g. {Text:"Strong name match", Importance:"high"}.
	Reasons      []ReasonItem `json:"reasons,omitempty"`
}

// IntelligenceReport is the full pre-merge analysis stored against a duplicate group.
type IntelligenceReport struct {
	RecommendedPrimary int64          `json:"recommended_primary"`
	ReadinessScore     float64        `json:"readiness_score"`
	Reasoning          []string       `json:"reasoning"`
	RiskFlags          []string       `json:"risk_flags"`
	Simulation         SimulationPreview `json:"simulation"`
	Breakdown          *FieldBreakdown   `json:"breakdown,omitempty"`
	// Conflicts are structured incompatibilities detected between the customers.
	// Each item carries type, severity, and a blocking flag used to gate bulk merges.
	Conflicts        []ConflictItem `json:"conflicts,omitempty"`
	// ConflictSeverity is "high", "medium", "low", or "" (no conflicts).
	// High-severity conflicts override the confidence-based risk level.
	ConflictSeverity string         `json:"conflict_severity,omitempty"`
	// Summary is a one-line plain-English explanation of the overall confidence,
	// e.g. "Likely the same customer based on matching name and address."
	Summary    string    `json:"summary,omitempty"`
	ComputedAt time.Time `json:"computed_at"`
}

// ToRawJSON marshals the report to bytes suitable for JSONB storage.
func (r *IntelligenceReport) ToRawJSON() ([]byte, error) {
	return json.Marshal(r)
}

// FromRawJSON deserializes a stored JSONB blob back to a report.
func FromRawJSON(b []byte) (*IntelligenceReport, error) {
	var r IntelligenceReport
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("unmarshal intelligence report: %w", err)
	}
	return &r, nil
}

// Analyzer generates IntelligenceReports from cached customer data.
// Instantiate once and reuse — it is stateless.
type Analyzer struct{}

func NewAnalyzer() *Analyzer { return &Analyzer{} }

// Analyze produces a complete IntelligenceReport for a cluster of customers.
// Requires at least 2 customers.
func (a *Analyzer) Analyze(customers []models.CustomerCache) (*IntelligenceReport, error) {
	if len(customers) < 2 {
		return nil, fmt.Errorf("intelligence: need at least 2 customers, got %d", len(customers))
	}

	primaryID := recommendPrimary(customers)
	readiness, riskFlags, reasoning := scoreReadiness(customers, primaryID)
	sim := buildSimulation(customers, primaryID)

	return &IntelligenceReport{
		RecommendedPrimary: primaryID,
		ReadinessScore:     readiness,
		Reasoning:          reasoning,
		RiskFlags:          riskFlags,
		Simulation:         sim,
		ComputedAt:         time.Now().UTC(),
	}, nil
}
