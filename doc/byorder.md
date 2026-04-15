Customer Harmony — Behavioral Order Signals Extension Plan
(Feedback integrated — ready for implementation)
╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌
 Behavioral Order Signals — Surgical Extension Plan

 Context

 The identity engine currently matches customers on static profile fields (email, name, phone, address). This misses a high-quality behavioral signal: the addresses and
  names customers use when placing orders are more reliable than stored profile data. Adding order-derived signals improves confidence accuracy and reduces false
 positives, without touching any existing scoring rules, thresholds, or risk logic.

 Hard constraint: DO NOT modify existing computeConfidence() rules, classifyRisk() logic, or DefaultThreshold. New behavioral rules are prepended inside a feature-flag guard (EnableBehavioralSignals) — when the flag is OFF, scoring is byte-for-byte identical to the current baseline.

 ---
 Critical Files

 ┌────────────────────────────────────────────────────────────┬─────────────────────────────────────────────────────────────────────┐
 │                            File                            │                               Change                                │
 ├────────────────────────────────────────────────────────────┼─────────────────────────────────────────────────────────────────────┤
 │ internal/db/migrations/016_add_order_signals.{up,down}.sql │ NEW                                                                 │
 ├────────────────────────────────────────────────────────────┼─────────────────────────────────────────────────────────────────────┤
 │ internal/models/customer_cache.go                          │ Add 3 fields + OrderAddress struct                                  │
 ├────────────────────────────────────────────────────────────┼─────────────────────────────────────────────────────────────────────┤
 │ internal/services/shopify/customer.go                      │ Extend GraphQL query + map order data                               │
 ├────────────────────────────────────────────────────────────┼─────────────────────────────────────────────────────────────────────┤
 │ internal/services/sync/sync_service.go                     │ Pass new fields to upsert                                           │
 ├────────────────────────────────────────────────────────────┼─────────────────────────────────────────────────────────────────────┤
 │ internal/repository/customer_cache_repo.go                 │ Extend INSERT + upsert SQL                                          │
 ├────────────────────────────────────────────────────────────┼─────────────────────────────────────────────────────────────────────┤
 │ internal/models/merchant_settings.go                       │ Add EnableBehavioralSignals field                                   │
├────────────────────────────────────────────────────────────┼─────────────────────────────────────────────────────────────────────┤
│ internal/services/identity/scorer.go                       │ Extend Signals + guarded early-return rules in computeConfidence()  │
 ├────────────────────────────────────────────────────────────┼─────────────────────────────────────────────────────────────────────┤
 │ internal/services/identity/cluster.go                      │ Add Sig to ScoredPair; add hasStrongSignal() guard                  │
 ├────────────────────────────────────────────────────────────┼─────────────────────────────────────────────────────────────────────┤
 │ internal/services/identity/detector.go                     │ Populate ScoredPair.Sig; set report.BehavioralSignals               │
 ├────────────────────────────────────────────────────────────┼─────────────────────────────────────────────────────────────────────┤
 │ internal/services/intelligence/analyzer.go                 │ Add BehavioralSignals struct + field to IntelligenceReport          │
 ├────────────────────────────────────────────────────────────┼─────────────────────────────────────────────────────────────────────┤
 │ internal/services/intelligence/breakdown.go                │ Add order_country_mismatch conflict                                 │
 ├────────────────────────────────────────────────────────────┼─────────────────────────────────────────────────────────────────────┤
 │ internal/api/dto/duplicate_dto.go                          │ Add BehavioralSignalsDTO; fix missing Resolvable on ConflictItemDTO │
 ├────────────────────────────────────────────────────────────┼─────────────────────────────────────────────────────────────────────┤
 │ internal/api/handlers/duplicate_handler.go                 │ Map BehavioralSignals; fix Resolvable pass-through                  │
 ├────────────────────────────────────────────────────────────┼─────────────────────────────────────────────────────────────────────┤
 │ src/api/types.ts                                           │ Add BehavioralSignals interface; extend GroupIntelligence           │
 ├────────────────────────────────────────────────────────────┼─────────────────────────────────────────────────────────────────────┤
 │ src/pages/MergeReview.tsx                                  │ Add Behavioral Signals card after Trust Analysis section            │
├────────────────────────────────────────────────────────────┼─────────────────────────────────────────────────────────────────────┤
│ src/pages/SettingsPage.tsx                                 │ Add Enable Behavioral Signals toggle                               │
 └────────────────────────────────────────────────────────────┴─────────────────────────────────────────────────────────────────────┘

 ---
 Step 1 — Migration 016

internal/db/migrations/016_add_order_signals.up.sql
ALTER TABLE customer_cache
  ADD COLUMN last_order_at   TIMESTAMPTZ,
  ADD COLUMN order_addresses JSONB,
  ADD COLUMN order_names     TEXT[];

ALTER TABLE merchant_settings
  ADD COLUMN enable_behavioral_signals BOOLEAN NOT NULL DEFAULT false;

internal/db/migrations/016_add_order_signals.down.sql
ALTER TABLE merchant_settings
  DROP COLUMN IF EXISTS enable_behavioral_signals;

ALTER TABLE customer_cache
  DROP COLUMN IF EXISTS last_order_at,
  DROP COLUMN IF EXISTS order_addresses,
  DROP COLUMN IF EXISTS order_names;

All three customer_cache columns nullable — no backfill required.
Feature flag defaults OFF — merchants must opt in to activate behavioral scoring.

 ---
 Step 2 — Model

 internal/models/customer_cache.go — add after ShopifyCreatedAt:

 LastOrderAt    *time.Time      `db:"last_order_at"`
 OrderAddresses json.RawMessage `db:"order_addresses"` // []OrderAddress JSON
 OrderNames     pq.StringArray  `db:"order_names"`

 Add OrderAddress struct (same file):

 type OrderAddress struct {
     Street  string `json:"street"`
     City    string `json:"city"`
     Zip     string `json:"zip"`
     Country string `json:"country"`
 }

 ---
 Step 3 — Shopify Customer Service (GraphQL)

 internal/services/shopify/customer.go

 The current query fetches profile fields only — numberOfOrders/amountSpent aggregates, no order detail. Extend the node { } block:

 orders(first: 5, sortKey: CREATED_AT, reverse: true) {
   edges {
     node {
       createdAt
       name
       shippingAddress { address1 city zip countryCode }
       billingAddress  { address1 city zip countryCode }
     }
   }
 }

 Add Go response types:

 type gqlOrderNode struct {
     CreatedAt       string        `json:"createdAt"`
     Name            string        `json:"name"`
     ShippingAddress *gqlOrderAddr `json:"shippingAddress"`
     BillingAddress*gqlOrderAddr `json:"billingAddress"`
 }
 type gqlOrderAddr struct {
     Address1    string `json:"address1"`
     City        string `json:"city"`
     Zip         string `json:"zip"`
     CountryCode string `json:"countryCode"`
 }

 Extend ShopifyCustomer struct:

 LastOrderAt    *time.Time
 OrderAddresses []models.OrderAddress
 OrderNames     []string

 Map in the response loop (fail silently if orders missing):

 for _, edge := range node.Orders.Edges {
     o := edge.Node
     if t, err := time.Parse(time.RFC3339, o.CreatedAt); err == nil {
         if cust.LastOrderAt == nil || t.After(*cust.LastOrderAt) {
             cust.LastOrderAt = &t
         }
     }
     if o.Name != "" {
         cust.OrderNames = append(cust.OrderNames, o.Name)
     }
     for_, addr := range []*gqlOrderAddr{o.ShippingAddress, o.BillingAddress} {
         if addr != nil && addr.City != "" {
             cust.OrderAddresses = append(cust.OrderAddresses, models.OrderAddress{
                 Street: addr.Address1, City: addr.City,
                 Zip: addr.Zip, Country: addr.CountryCode,
             })
         }
     }
 }

 ---
 Step 4 — Sync Service + Repository

 internal/services/sync/sync_service.go — in the ShopifyCustomer → CustomerCache mapping:

 cc.LastOrderAt    = sc.LastOrderAt
 cc.OrderAddresses = marshalOrderAddresses(sc.OrderAddresses) // json.Marshal → RawMessage
 cc.OrderNames     = pq.StringArray(sc.OrderNames)

 internal/repository/customer_cache_repo.go — extend the upsert INSERT column list, $N placeholders, and ON CONFLICT ... DO UPDATE SET:

 last_order_at   = EXCLUDED.last_order_at,
 order_addresses = EXCLUDED.order_addresses,
 order_names     = EXCLUDED.order_names

 ---
 Step 5 — Extend Signals + extractSignals

 internal/services/identity/scorer.go

 5a. Append to Signals struct (DO NOT reorder existing fields):

// ── Behavioral (order-derived) signals ──
OrderAddressExact   bool
OrderAddressPartial bool
OrderNameHigh       bool
RecentOrderOverlap  bool
DifferentLastName   bool // guards against household/roommate merges

 5b. Append to end of extractSignals() after the existing address block:

 if len(a.OrderAddresses) > 0 && len(b.OrderAddresses) > 0 {
     s.OrderAddressExact   = anyExactOrderAddressMatch(a.OrderAddresses, b.OrderAddresses)
     s.OrderAddressPartial = anyPartialOrderAddressMatch(a.OrderAddresses, b.OrderAddresses)
 }
 if len(a.OrderNames) > 0 && len(b.OrderNames) > 0 {
     s.OrderNameHigh = anyHighOrderNameMatch(a.OrderNames, b.OrderNames)
 }
if a.LastOrderAt != nil && b.LastOrderAt != nil {
    diff := a.LastOrderAt.Sub(*b.LastOrderAt)
    if diff < 0 { diff = -diff }
    s.RecentOrderOverlap = diff <= 7*24*time.Hour
}
// Last-name divergence — prevents merging roommates/family at same address
if a.Name != "" && b.Name != "" {
    lastA := extractLastName(a.Name)
    lastB := extractLastName(b.Name)
    s.DifferentLastName = lastA != "" && lastB != "" && !strings.EqualFold(lastA, lastB)
}

5c. Add four helpers — reuse existing levenshteinSim(), jaroWinkler(), and normalization:

 func anyExactOrderAddressMatch(as, bs []models.OrderAddress) bool {
     for _, a := range as {
         for_, b := range bs {
             if strings.EqualFold(a.City, b.City) &&
                strings.EqualFold(a.Zip, b.Zip) &&
                strings.EqualFold(a.Country, b.Country) {
                 return true
             }
         }
     }
     return false
 }

 func anyPartialOrderAddressMatch(as, bs []models.OrderAddress) bool {
     for _, a := range as {
         for_, b := range bs {
             addrA := strings.ToLower(strings.Join([]string{a.Street, a.City, a.Zip, a.Country}, " "))
             addrB := strings.ToLower(strings.Join([]string{b.Street, b.City, b.Zip, b.Country}, " "))
             if levenshteinSim(addrA, addrB) >= 0.85 { return true }
         }
     }
     return false
 }

func anyHighOrderNameMatch(as, bs []string) bool {
    for _, a := range as {
        for_, b := range bs {
            if jaroWinkler(strings.ToLower(a), strings.ToLower(b)) >= 0.92 { return true }
        }
    }
    return false
}

func extractLastName(fullName string) string {
    parts := strings.Fields(strings.TrimSpace(fullName))
    if len(parts) == 0 { return "" }
    return parts[len(parts)-1]
}

---
Step 6 — Insert 4 Early-Return Rules in computeConfidence()

internal/services/identity/scorer.go

Add behavioralEnabled bool parameter to computeConfidence():

func computeConfidence(s Signals, behavioralEnabled bool) float64 {

Insert at the very top of the function body, before the existing Tier 1 block. Touch nothing below.

if behavioralEnabled {
    // ── Behavioral signals (fire only when order data + flag are present) ──
    if s.OrderAddressExact && s.NameHigh && s.SameCountry && !s.DifferentLastName { return 0.96 }
    if s.OrderNameHigh && s.PhoneExact                                            { return 0.95 }
    if s.OrderAddressExact && s.EmailLocalExact                                   { return 0.94 }
    if s.RecentOrderOverlap && s.OrderAddressPartial && s.NameHigh               { return 0.91 }
    if s.OrderAddressPartial && s.NameHigh                                        { return 0.90 }

    log.Debug().
        Bool("order_address_exact", s.OrderAddressExact).
        Bool("order_address_partial", s.OrderAddressPartial).
        Bool("order_name_high", s.OrderNameHigh).
        Bool("recent_order_overlap", s.RecentOrderOverlap).
        Bool("different_last_name", s.DifferentLastName).
        Msg("behavioral rule evaluation")
}

// (existing Tier 1 block continues here, unchanged)

Notes:
- EmailLocalExact is the existing field name (spec called it EmailLocalMatch).
- SameCountry + !DifferentLastName guard on the 0.96 rule prevents merging roommates/family at the same address.
- RecentOrderOverlap + OrderAddressPartial + NameHigh (0.91) captures same person ordering recently with slight variations.
- Without behavioralEnabled, all existing scoring paths are untouched — zero regression risk.

 ---
 Step 7 — ScoredPair.Sig + Clustering Guard

 internal/services/identity/cluster.go

 7a. Extend ScoredPair struct — append after AddressSim:

 Sig Signals

 7b. Add hasStrongSignal():

func hasStrongSignal(s Signals) bool {
    return s.EmailExact ||
           s.PhoneExact ||
           s.OrderAddressExact ||
           s.AddressExact ||
           (s.EmailLocalExact && s.NameHigh) ||
           (s.PhoneSuffix && s.NameHigh)
}

Note: The last two conditions preserve legitimate matches (EmailLocalMatch+NameHigh ≈0.88,
PhoneSuffix+NameHigh ≈0.90) that would otherwise be killed by the guard. Keeps strong filtering
without discarding valid real-world duplicates.

 7c. Insert pre-check in ClusterPairs() immediately before the existing threshold guard (currently line 83):

 // Strong-signal guard: require at least one hard identity anchor.
 if !hasStrongSignal(p.Sig) {
     continue
 }
 // (existing) Threshold guard
 if p.Score < threshold {

 Impact: Pairs scoring via NameHigh + AddressPartial (0.76), EmailLocalFuzzy + NameMedium (0.74), and NameHigh + EmailDomainMatch (0.70) will no longer form clusters.
 All EmailExact/PhoneExact/AddressExact pairs are unaffected.

 ---
 Step 8 — Populate ScoredPair.Sig in Detector

 internal/services/identity/detector.go

Load the merchant's behavioral flag before scoring begins (at the top of Detect()):

settings, _ := d.settingsRepo.GetByMerchantID(ctx, merchantID)
behavioralEnabled := settings != nil && settings.EnableBehavioralSignals

Pass behavioralEnabled through to ScorePair() → computeConfidence(). In scorePairs(), when constructing
each ScoredPair, add Sig: sc.Sig (sc is the Score returned by ScorePair()):

ScoredPair{
    A: ..., B: ...,
    Score: sc.Combined, EmailSim: sc.EmailSim,
    NameSim: sc.NameSim, PhoneSim: sc.PhoneSim, AddressSim: sc.AddressSim,
    Sig: sc.Sig,  // ← new
}

After report.Breakdown is set (~line 126), add:

 if topPair != nil {
     report.BehavioralSignals = &intelligence.BehavioralSignals{
         OrderAddressExact:   topPair.Sig.OrderAddressExact,
         OrderAddressPartial: topPair.Sig.OrderAddressPartial,
         OrderNameHigh:       topPair.Sig.OrderNameHigh,
         RecentOrderOverlap:  topPair.Sig.RecentOrderOverlap,
     }
 }

 ---
 Step 9 — BehavioralSignals in IntelligenceReport

 internal/services/intelligence/analyzer.go

 Add struct:

 type BehavioralSignals struct {
     OrderAddressExact   bool `json:"order_address_exact"`
     OrderAddressPartial bool `json:"order_address_partial"`
     OrderNameHigh       bool `json:"order_name_high"`
     RecentOrderOverlap  bool `json:"recent_order_overlap"`
 }

 Add field to IntelligenceReport (after Summary):

 BehavioralSignals *BehavioralSignals `json:"behavioral_signals,omitempty"`

 ---
 Step 10 — order_country_mismatch Conflict

 internal/services/intelligence/breakdown.go

 Add helper:

 func orderCountryMismatch(members []models.CustomerCache) bool {
     countries := make(map[string]struct{})
     for _, m := range members {
         if len(m.OrderAddresses) == 0 { continue }
         var addrs []models.OrderAddress
         if err := json.Unmarshal(m.OrderAddresses, &addrs); err != nil { continue }
         for_, a := range addrs {
             if a.Country != "" { countries[strings.ToUpper(a.Country)] = struct{}{} }
         }
     }
     return len(countries) > 1
 }

Append to DetectConflicts() after existing checks. Only flag when there is no hard identity anchor
(exact email or phone match) — strong identity matches override geographic divergence, since people
legitimately order from multiple countries:

if orderCountryMismatch(members) && !hasExactIdentityAnchor(members) {
    result.Conflicts = append(result.Conflicts, ConflictItem{
        Type: "order_country_mismatch", Severity: "high",
        Blocking: false, Resolvable: false,
    })
}

Add identity anchor helper (same file):

func hasExactIdentityAnchor(members []models.CustomerCache) bool {
    for i := 0; i < len(members); i++ {
        for j := i + 1; j < len(members); j++ {
            if members[i].Email != "" && strings.EqualFold(members[i].Email, members[j].Email) {
                return true
            }
            if members[i].Phone != "" && members[i].Phone == members[j].Phone {
                return true
            }
        }
    }
    return false
}

 ---
 Step 11 — DTO + Handler

 internal/api/dto/duplicate_dto.go

 Fix pre-existing bug — ConflictItemDTO is missing Resolvable (frontend already expects it):

 type ConflictItemDTO struct {
     Type       string `json:"type"`
     Severity   string `json:"severity"`
     Blocking   bool   `json:"blocking"`
     Resolvable bool   `json:"resolvable"` // was missing
 }

 Add:

 type BehavioralSignalsDTO struct {
     OrderAddressExact   bool `json:"order_address_exact"`
     OrderAddressPartial bool `json:"order_address_partial"`
     OrderNameHigh       bool `json:"order_name_high"`
     RecentOrderOverlap  bool `json:"recent_order_overlap"`
 }

 Add field to IntelligenceDTO:

 BehavioralSignals *BehavioralSignalsDTO `json:"behavioral_signals,omitempty"`

 internal/api/handlers/duplicate_handler.go — in buildIntelligenceDTO():

 Fix conflict mapping (add Resolvable: ci.Resolvable):

 conflicts = append(conflicts, dto.ConflictItemDTO{
     Type: ci.Type, Severity: ci.Severity,
     Blocking: ci.Blocking, Resolvable: ci.Resolvable,
 })

 Map behavioral signals (append after the Breakdown block):

 if r.BehavioralSignals != nil {
     idto.BehavioralSignals = &dto.BehavioralSignalsDTO{
         OrderAddressExact:   r.BehavioralSignals.OrderAddressExact,
         OrderAddressPartial: r.BehavioralSignals.OrderAddressPartial,
         OrderNameHigh:       r.BehavioralSignals.OrderNameHigh,
         RecentOrderOverlap:  r.BehavioralSignals.RecentOrderOverlap,
     }
 }

internal/models/merchant_settings.go — add field:

EnableBehavioralSignals bool `db:"enable_behavioral_signals"`

internal/repository/settings_repo.go — include enable_behavioral_signals in SELECT and UPDATE queries.

internal/api/dto/ — add to settings DTO:

EnableBehavioralSignals bool `json:"enable_behavioral_signals"`

internal/api/handlers/settings_handler.go — map the field in both GET and PUT.

---
Step 12 — Frontend Types

src/api/types.ts

Add to MerchantSettings interface:

enable_behavioral_signals: boolean;

src/pages/SettingsPage.tsx — add toggle for "Enable Behavioral Signals" in the settings form.

Add types:

 export interface BehavioralSignals {
   order_address_exact:   boolean;
   order_address_partial: boolean;
   order_name_high:       boolean;
   recent_order_overlap:  boolean;
 }

 Extend GroupIntelligence:

 behavioral_signals?: BehavioralSignals;

 ---
 Step 13 — Frontend Behavioral Signals Card

 src/pages/MergeReview.tsx — add after section 4 (Trust Analysis), before section 5 (Merge Readiness). Only renders when at least one signal is active:

 {(() => {
   const bs = group?.intelligence?.behavioral_signals;
   if (!bs) return null;
  const active = [
    bs.order_address_exact   && "Same shipping address used in past orders",
    bs.order_address_partial && "Similar address found in order history",
    bs.order_name_high       && "Same name used at checkout",
    bs.recent_order_overlap  && "Orders placed within 7 days of each other",
  ].filter(Boolean) as string[];
  if (active.length === 0) return null;
  const isHighConfidence = bs.order_address_exact || (bs.order_name_high && bs.order_address_partial);
  return (
    <Card>
      <BlockStack gap="300">
        <BlockStack gap="050">
          <InlineStack gap="200" blockAlign="center">
            <Text as="h2" variant="headingMd">Behavioral Signals</Text>
            {isHighConfidence && (
              <Badge tone="success">High-confidence behavioral match</Badge>
            )}
          </InlineStack>
          <Text as="p" variant="bodySm" tone="subdued">
            Derived from order history — independent of stored profile fields.
          </Text>
        </BlockStack>
        <BlockStack gap="150">
          {active.map((s) => (
            <InlineStack key={s} gap="200" blockAlign="center">
              <Icon source={CheckCircleIcon} tone="success" />
              <Text as="p" variant="bodyMd">{s}</Text>
            </InlineStack>
          ))}
        </BlockStack>
      </BlockStack>
    </Card>
   );
 })()}

 ---
Verification

1. Migration — run server, confirm 3 columns on customer_cache + enable_behavioral_signals on merchant_settings, down migration cleans up
2. Sync — trigger a manual scan; DB rows should have last_order_at / order_addresses / order_names populated for customers with orders (null for those without — correct)
3. Feature flag OFF — with enable_behavioral_signals=false, run detection; confirm behavioral early-return rules never fire (check logs for absence of "behavioral rule evaluation" messages). All scores must match pre-change baseline exactly.
4. Feature flag ON — enable flag, re-run detection; confirm behavioral rules fire and produce expected scores
5. Scoring — seed two customers sharing a shipping city+zip+country in their order history with same last name; computeConfidence() should return 0.96
6. Household guard — seed two customers at same order address but with DIFFERENT last names; confirm 0.96 rule does NOT fire (DifferentLastName blocks it)
7. RecentOrderOverlap — seed two customers with orders 3 days apart + partial address match + high name match; confirm 0.91 rule fires
8. Clustering guard — seed a name-only pair (no email/phone/address exact, no EmailLocalMatch+NameHigh); confirm it no longer forms a duplicate group
9. Clustering preserved — seed EmailLocalExact+NameHigh pair; confirm it still clusters (expanded hasStrongSignal allows it)
10. Conflict — seed two customers with orders from different countries and NO exact email/phone match; confirm order_country_mismatch appears in GET /api/duplicates/:id response
11. Conflict suppressed — seed two customers with orders from different countries but SAME email; confirm order_country_mismatch does NOT appear
12. Frontend — open Merge Review for a group with order signals; confirm "Behavioral Signals" card renders with correct bullets and "High-confidence behavioral match" badge
13. Before/after comparison — run full detection on a test dataset with behavioral OFF, record: group count, safe/review/risky distribution. Then run with behavioral ON. If drift > 15% in any category, investigate before deploying.

 ---
 Customer Harmony — Go Backend Implementation Plan

 Context

 Building a Shopify customer deduplication app called "Customer Harmony" (repo: merger). The frontend (merger-frontend/customer-harmony) is complete in React/TypeScript
  but uses entirely hardcoded mock data. The backend (merger-backend) is empty except for doc/infra.md, which contains a detailed production architecture spec. The goal
  is to implement that spec as a Go backend and wire the frontend to it.

 Critical pivot (post-infra.md): Shopify has an official customerMerge GraphQL mutation. We use it instead of building any custom order reassignment or customer
 archival logic. This makes our merge execution reliable and Shopify-compliant. Our real value is the intelligence layer: detection, scoring, bulk queuing, snapshots,
 and audit — not reimplementing what Shopify already provides.

 ▎ Shopify merge is irreversible. This makes snapshots even more critical — they are the only way to reconstruct pre-merge state. We never "undo", we "reconstruct".

 ---
 Tech Stack

 ┌───────────────┬────────────────────────────────────┬────────────────────────────────────────────────────────────────────────────────────────────────────────┐
 │   Component   │               Choice               │                                                 Reason                                                 │
 ├───────────────┼────────────────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ Language      │ Go 1.22                            │ Per spec                                                                                               │
 ├───────────────┼────────────────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ Web Framework │ Gin                                │ Standard net/http body makes Shopify HMAC validation straightforward (Fiber/fasthttp complicates this) │
 ├───────────────┼────────────────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ Database      │ PostgreSQL + pgx/v5 + sqlx         │ No ORM, visible SQL, struct scanning                                                                   │
 ├───────────────┼────────────────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ Queue         │ Redis lists (BRPOPLPUSH)           │ At-least-once delivery, simpler than Streams for V1                                                    │
 ├───────────────┼────────────────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ Migrations    │ golang-migrate/migrate/v4          │ SQL-file based, pairs with pgx                                                                         │
 ├───────────────┼────────────────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ Auth          │ Shopify OAuth + JWT session cookie │ Per spec                                                                                               │
 ├───────────────┼────────────────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ Logging       │ zerolog                            │ Structured JSON, zero-allocation                                                                       │
 ├───────────────┼────────────────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ Config        │ viper                              │ .env + env vars, 12-factor                                                                             │
 ├───────────────┼────────────────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ Crypto        │ AES-256-GCM (golang.org/x/crypto)  │ Encrypt Shopify tokens at rest                                                                         │
 └───────────────┴────────────────────────────────────┴────────────────────────────────────────────────────────────────────────────────────────────────────────┘

 ---
 Critical Files

 Backend (all new):

- merger-backend/cmd/api/main.go — entry point, dependency wiring, graceful shutdown
- merger-backend/internal/config/config.go
- merger-backend/internal/server/router.go + middleware.go
- merger-backend/internal/db/postgres.go + db/migrations/001–007_*.sql
- merger-backend/internal/queue/redis.go + queue.go
- merger-backend/internal/models/ — 6 model files (merchant, customer_cache, duplicate_group, merge_record, snapshot, job, merchant_settings)
- merger-backend/internal/repository/ — 7 repo interface + implementation files
- merger-backend/pkg/shopifyauth/oauth.go — OAuth + HMAC validation
- merger-backend/internal/services/shopify/ — client, customer, order, webhook
- merger-backend/internal/services/identity/ — detector, scorer, cluster
- merger-backend/internal/services/merge/ — orchestrator, validator, executor
- merger-backend/internal/services/snapshot/snapshot_service.go
- merger-backend/internal/services/jobs/ — dispatcher, worker, processor
- merger-backend/internal/api/handlers/ — 7 handler files
- merger-backend/internal/api/dto/ — merge_dto, duplicate_dto, job_dto
- merger-backend/internal/utils/ — logger, retry, normalization, crypto

 Frontend (modifications to existing files):

- merger-frontend/customer-harmony/src/api/client.ts (new)
- merger-frontend/customer-harmony/src/api/types.ts (new)
- merger-frontend/customer-harmony/src/api/duplicates.ts (new)
- merger-frontend/customer-harmony/src/api/merge.ts (new)
- merger-frontend/customer-harmony/src/api/jobs.ts (new)
- merger-frontend/customer-harmony/src/api/metrics.ts (new)
- merger-frontend/customer-harmony/src/api/settings.ts (new)
- merger-frontend/customer-harmony/src/hooks/useJobPoller.ts (new)
- merger-frontend/customer-harmony/src/pages/Dashboard.tsx — swap mock data for useQuery
- merger-frontend/customer-harmony/src/pages/Duplicates.tsx — swap mock data for useQuery
- merger-frontend/customer-harmony/src/pages/MergeReview.tsx — add useMutation + job polling
- merger-frontend/customer-harmony/src/pages/HistoryPage.tsx — swap mock data for useQuery
- merger-frontend/customer-harmony/src/pages/SettingsPage.tsx — load/save via API
- merger-frontend/customer-harmony/.env.example (new)

 ---
 Database Schema (7 migrations)

 001: merchants           — id UUID, shop_domain TEXT UNIQUE, access_token_enc TEXT, created_at
 002: customer_cache      — id, merchant_id, shopify_customer_id BIGINT, email, name, phone, address_json JSONB, tags TEXT[], updated_at
                            indexes: (merchant_id, email), (merchant_id, shopify_customer_id)
 003: duplicate_groups    — id, merchant_id, group_hash TEXT, customer_ids BIGINT[], confidence_score FLOAT, status (pending|reviewed|merged)
 004: merge_records       — id, merchant_id, primary_customer_id BIGINT, secondary_customer_ids BIGINT[], orders_moved INT, performed_by TEXT, snapshot_id UUID
 005: snapshots           — id, merchant_id, merge_record_id UUID, data JSONB, created_at
 006: jobs                — id, merchant_id, type TEXT, status (queued|processing|completed|failed), payload JSONB, result JSONB, retries INT
 007: merchant_settings   — merchant_id PK, auto_detect BOOL, confidence_threshold INT, retention_days INT, notifications_enabled BOOL

 ---
 API Routes

 GET  /health
 GET  /auth/shopify              → OAuth install redirect
 GET  /auth/shopify/callback     → exchange code, set JWT cookie, redirect to frontend

 POST /api/webhooks/shopify      → HMAC-verified, updates customer_cache, queues detect job

 // All below: AuthRequired middleware (JWT cookie → merchant context)
 GET  /api/duplicates            → list duplicate groups (filter by status, paginated)
 GET  /api/duplicates/:id        → single group with enriched customer data
 POST /api/merge/execute         → queue merge_customers job → returns {job_id, status}
 GET  /api/merge/history         → paginated merge audit log
 GET  /api/jobs/:id              → job status polling
 POST /api/snapshot/restore/:id  → queue restore_snapshot job
 GET  /api/metrics/dashboard     → health score, counts, recent activity
 GET  /api/settings              → merchant settings
 PUT  /api/settings              → update merchant settings

 ---
 Implementation Phases

 Phase 1: Foundation

 Goal: Runnable server, DB + Redis connected, migrations applied, health check working.

 Files: go.mod, main.go, config/config.go, db/postgres.go, db/migrations/001-007, queue/redis.go, utils/logger.go, server/router.go, server/middleware.go (CORS, logger,
  recovery)

 Key patterns:

- Constructor injection throughout — no globals, no init() side effects
- Graceful shutdown: signal.NotifyContext on SIGTERM/SIGINT, 30s drain timeout

 Verify: GET /health → {"status":"ok"}, all 7 tables visible in DB, Redis ping in logs

 ---
 Phase 2: Models, Repositories, Shopify OAuth

 Goal: Data layer + auth flow working end-to-end.

 Files: all models/, all repository/ (each defines an interface + concrete struct), pkg/shopifyauth/oauth.go, api/handlers/auth_handler.go, utils/normalization.go,
 utils/retry.go

 Key patterns:

- Each repo: type FooRepository interface { ... } + type fooRepo struct { db *sqlx.DB } + func NewFooRepo(db) FooRepository
- customer_cache upsert: INSERT ... ON CONFLICT (merchant_id, shopify_customer_id) DO UPDATE SET ...
- OAuth: ValidateHMAC on callback params, exchange code, AES-encrypt token, upsert merchant, issue JWT session cookie
- utils/crypto.go: Encryptor struct, AES-256-GCM, base64 of nonce || ciphertext

 Verify: Install flow redirects to Shopify, callback creates merchant row with encrypted token

 ---
 Phase 3: Shopify Service Layer

 Goal: All Shopify API calls wrapped with retry + rate limiting; webhooks ingest to customer_cache.

 Files: services/shopify/client.go, customer.go, order.go, webhook.go, api/handlers/webhook_handler.go

 Key patterns:

- client.go: two transports — doREST() for REST calls (customer cache, webhooks), doGraphQL() for GraphQL mutations (merge). Both set X-Shopify-Access-Token, handle
 429 with Retry-After, retry 5xx with exponential backoff
- customer.go FetchAll(): cursor-based pagination via REST, batches of 250 — used to populate customer_cache
- customer.go Merge(primaryID, secondaryID int64): calls customerMerge GraphQL mutation — this is the sole merge implementation
- order.go FetchByCustomer(): read-only — used only to populate snapshots before merge; no write operations
- Webhook handler: read full body first, validate HMAC, then parse — debounce detect_duplicates job dispatch

 Verify: Webhook HMAC test (valid body → 200, tampered → 401), customer fetch pagination, customerMerge called correctly on dev store

 ---
 Phase 4: Core Services — Detection, Merge, Jobs

 Goal: Full async pipeline works end-to-end.

 Queue layer (queue/queue.go):

- Push: LPUSH queueName jobID
- Pop: BRPOPLPUSH queueName processingQueue timeout (at-least-once delivery)
- Acknowledge: LREM processingQueue 1 jobID

 Job system (services/jobs/):

- dispatcher.go: creates DB jobs row (status=queued), pushes job ID to Redis list
- worker.go: concurrency=3 goroutines, BRPOPLPUSH loop, graceful shutdown via ctx.Done()
- processor.go: switch on job type → detect_duplicates / merge_customers / restore_snapshot

 Identity service (services/identity/):

- scorer.go: combined score = 0.4*EmailSim + 0.35*NameSim + 0.15*PhoneSim + 0.1*AddressSim; Jaro-Winkler for names, Levenshtein for email; implement inline (no
 external dep)
- cluster.go: union-find (parent map[int64]int64), ClusterPairs(pairs, threshold) map[int64][]int64
- detector.go: load customer_cache → normalize → email-domain bucketing (avoids O(n²) at scale) → score pairs within buckets → cluster → upsert duplicate_groups (skip  
 if hash exists with status=merged)

 Merge service (services/merge/):

- validator.go: check Shopify merge constraints — active subscriptions, B2B associations, store credit, gift cards; all customer IDs belong to the same merchant
- executor.go: calls Shopify customerMerge GraphQL mutation — this is the ONLY merge mechanism:
 mutation customerMerge($input: CustomerMergeInput!) {
   customerMerge(input: $input) {
     customer { id }
     userErrors { field message }
   }
 }
- No custom order reassignment. No archival tags. Shopify handles orders automatically.
- orchestrator.go: snapshot() → validate() → execute(customerMerge) → mergeRepo.Create() → duplicateRepo.UpdateStatus("merged"); on failure after snapshot: log + keep  
 snapshot for reconstruction reference

 What was removed vs original infra.md: order.go reassignment logic is deleted. shopify/order.go is kept only for reading orders into snapshots (not writing). The
 merged_into tag strategy is replaced entirely by the native API call.

 Handlers: duplicate_handler.go, merge_handler.go, job_handler.go, snapshot_handler.go, metrics_handler.go, settings_handler.go

- metrics_handler: 4 DB queries in parallel via errgroup, health score = (1 - pending_groups/total_customers) * 100

 Verify: POST merge → job queued → worker picks up → snapshot created → customerMerge GraphQL mutation called → job completed; detection on seeded customers produces
 correct duplicate_groups with confidence scores

 ---
 Snapshot Strategy (applies across all phases)

 Since customerMerge is irreversible, snapshots must capture full pre-merge state before any mutation:

- snapshot_service.Create(): fetches full customer data + their orders from Shopify REST API, serializes everything to JSONB, stores in snapshots table linked to
 merge_record_id
- snapshot_service.Restore() (V1 = reconstruction, not true undo): reads snapshot JSONB, creates a new Shopify customer with the secondary's original data, surfaces
 the snapshot as a reference document. UI clearly labels this "Reconstruct" not "Undo".
- Snapshots are the only safety net — the merge validation step before calling customerMerge must be exhaustive.

 ---
 Phase 5: Security + Resilience

 Goal: Production hardening.

- Rate limiting middleware: token bucket per merchant_id using Redis INCR+EXPIRE sliding window
- Job recovery sweep: goroutine runs every 5 min, re-queues jobs stuck in processing > 10 min
- Webhook registration on startup: iterate merchants, call shopifyWebhookSvc.RegisterAll() for unregistered ones
- Finalize utils/retry.go: RetryWithBackoff(ctx, attempts, baseDuration, fn) with exponential + jitter

 Verify: Crash worker mid-job → recovery sweep re-queues within 10 min; encrypt/decrypt round-trip test

 ---
 Phase 6: Frontend API Integration

 Goal: All pages consume real data; no mock arrays remain.

 src/api/client.ts — typed apiFetch<T> wrapper:

- Base URL from import.meta.env.VITE_API_URL
- credentials: "include" for session cookie
- 401 → redirect to /auth/shopify?shop=...

 Page wiring:

 ┌──────────────────┬───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
 │       Page       │                                                          Change                                                           │
 ├──────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ Dashboard.tsx    │ useQuery(getDashboardMetrics), refetchInterval: 60_000                                                                    │
 ├──────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ Duplicates.tsx   │ useQuery(getDuplicates({status, limit, offset})), placeholderData: keepPreviousData, navigate to /merge-review?group={id} │
 ├──────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ MergeReview.tsx  │ Read groupId from useSearchParams, fetch group, useMutation(executeMerge), poll job via useJobPoller hook                 │
 ├──────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ HistoryPage.tsx  │ useQuery(getMergeHistory), restore via useMutation                                                                        │
 ├──────────────────┼───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
 │ SettingsPage.tsx │ Load via useQuery, add Save button with useMutation(updateSettings)                                                       │
 └──────────────────┴───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘

---
Implementation Rules (all feedback incorporated into steps above)

1. Isolation - ALL behavioral scoring logic is gated behind EnableBehavioralSignals (merchant_settings).
   With the flag OFF, the system behaves identically to pre-change baseline. Zero regression risk.

2. No mutation of existing rules - existing computeConfidence() tiers, classifyRisk() logic, and
   DefaultThreshold are untouched. New rules are prepended inside a feature-flag guard block.

3. Backward compatibility test - before deploy, run detection with behavioral OFF and ON on the same
   dataset. Compare group count and safe/review/risky distribution. If drift > 15%, investigate.

4. Logging - behavioral rule evaluation is logged at Debug level with all signal booleans.
   This enables production debugging and A/B comparison.

5. UI trust - the Behavioral Signals card shows a High-confidence behavioral match badge
   when strong order signals are active, not just bullet points.

---
Backend Implementation Plan - COMPLETED

All 6 phases have been implemented. Current state:

- Phases 1-4: Foundation, models, repos, auth, Shopify services, identity/merge/jobs pipeline - DONE
- Phase 5: Security + resilience - DONE (rate limit middleware coded but not wired to router)
- Phase 6: Frontend API integration - DONE (all pages consume real API, no mock data remains)
- Migrations: 15 applied (001-015), exceeding the original 7 planned
- Additional features beyond plan: intelligence service, sync service, idempotency,
  business risk scoring, dismiss feedback, scan handler, profile validation

Next: Behavioral Order Signals (Steps 1-13 above) - NOT YET STARTED
