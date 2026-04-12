// Package intelligence provides pre-merge analysis for duplicate customer groups.
// All analysis runs from customer_cache data — no Shopify API calls required.
package intelligence

import (
	"encoding/json"
	"fmt"
	"time"

	"merger/backend/internal/models"
)

// IntelligenceReport is the full pre-merge analysis stored against a duplicate group.
type IntelligenceReport struct {
	RecommendedPrimary int64             `json:"recommended_primary"`
	ReadinessScore     float64           `json:"readiness_score"`
	Reasoning          []string          `json:"reasoning"`
	RiskFlags          []string          `json:"risk_flags"`
	Simulation         SimulationPreview `json:"simulation"`
	ComputedAt         time.Time         `json:"computed_at"`
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
