Good—this is the handoff moment. So I’m going to give you something your dev team can actually implement **without guessing**.

No fluff. No vague “service layer” talk. This is a **production-ready backend structure**, aligned with:

* Shopify API realities (no guaranteed native merge API)
* Async processing (required)
* Data safety (snapshots, audit)
* Scaling from day 1

---

# 🏗️ BACKEND ARCHITECTURE (FINAL — PRODUCTION READY)

## 🧠 Core Design Principles

1. **Never trust synchronous operations**
   → All merges + detection = async jobs

2. **Never mutate without backup**
   → Snapshot before every merge

3. **Shopify is source of truth**
   → You cache, not own data

4. **Design for failure**
   → Every step must be retryable

---

# 📁 FINAL DIRECTORY STRUCTURE

```id="a9qk3p"
backend/
├── cmd/
│   └── api/
│       └── main.go
│
├── internal/
│   ├── config/
│   │   └── config.go
│
│   ├── server/
│   │   ├── router.go
│   │   └── middleware.go
│
│   ├── api/
│   │   ├── handlers/
│   │   │   ├── auth_handler.go
│   │   │   ├── duplicate_handler.go
│   │   │   ├── merge_handler.go
│   │   │   ├── job_handler.go
│   │   │   ├── snapshot_handler.go
│   │   │   └── metrics_handler.go
│   │   │
│   │   └── dto/
│   │       ├── merge_dto.go
│   │       ├── duplicate_dto.go
│   │       └── job_dto.go
│
│   ├── services/
│   │   ├── identity/
│   │   │   ├── detector.go
│   │   │   ├── scorer.go
│   │   │   └── cluster.go
│   │   │
│   │   ├── merge/
│   │   │   ├── orchestrator.go
│   │   │   ├── validator.go
│   │   │   └── executor.go
│   │   │
│   │   ├── snapshot/
│   │   │   └── snapshot_service.go
│   │   │
│   │   ├── jobs/
│   │   │   ├── worker.go
│   │   │   ├── dispatcher.go
│   │   │   └── processor.go
│   │   │
│   │   ├── metrics/
│   │   │   └── metrics_service.go
│   │   │
│   │   └── shopify/
│   │       ├── client.go
│   │       ├── customer.go
│   │       ├── order.go
│   │       └── webhook.go
│
│   ├── repository/
│   │   ├── merchant_repo.go
│   │   ├── customer_cache_repo.go
│   │   ├── duplicate_repo.go
│   │   ├── merge_repo.go
│   │   ├── snapshot_repo.go
│   │   └── job_repo.go
│
│   ├── models/
│   │   ├── merchant.go
│   │   ├── customer_cache.go
│   │   ├── duplicate_group.go
│   │   ├── merge_record.go
│   │   ├── snapshot.go
│   │   └── job.go
│
│   ├── db/
│   │   ├── migrations/
│   │   └── postgres.go
│
│   ├── queue/
│   │   ├── redis.go
│   │   └── queue.go
│
│   └── utils/
│       ├── logger.go
│       ├── retry.go
│       └── normalization.go
│
├── pkg/
│   └── shopifyauth/
│       └── oauth.go
│
└── go.mod
```

---

# 🧩 CORE SERVICES (WHAT EACH DOES)

## 1️⃣ Identity Service (Duplicate Detection)

### Responsibilities

* Normalize customers
* Generate feature vectors
* Score similarity
* Cluster duplicates

---

### Key Flow

```go
customers → normalize → pair compare → score → cluster → store groups
```

---

### Files

```go
detector.go     // runs detection job
scorer.go       // similarity logic
cluster.go      // grouping logic (union-find)
```

---

## 2️⃣ Merge Service (CRITICAL SYSTEM)

Split into 3 parts to avoid chaos:

---

### ✅ validator.go

Checks Shopify constraints:

* subscription contracts
* B2B associations
* payment methods

```go
func ValidateMerge(customers []Customer) error
```

---

### ✅ orchestrator.go

Controls full flow:

```go
func ExecuteMerge(ctx, req) {
  snapshot()
  validate()
  execute()
  audit()
}
```

---

### ✅ executor.go

Handles actual logic:

* reassign orders
* merge metadata
* archive secondary

---

## 3️⃣ Snapshot Service (YOUR SAFETY NET)

### Purpose

* Store full pre-merge state
* Enable restore

---

### Data Stored

```json
{
  "customers": [...],
  "orders": [...],
  "tags": [...]
}
```

---

## 4️⃣ Job System (NON-NEGOTIABLE)

### Components

* dispatcher.go → pushes jobs
* worker.go → pulls jobs
* processor.go → handles job logic

---

### Job Types

```text
- detect_duplicates
- merge_customers
- restore_snapshot
```

---

## 5️⃣ Shopify Service Layer

### Files

```go
customer.go → fetch/update customers
order.go    → reassign orders
webhook.go  → handle updates
```

---

### MUST include

* retry logic
* rate limit handling

---

# 🗄️ DATABASE (FINAL STRUCTURE)

## merchants

```sql
id, shop_domain, access_token, created_at
```

---

## customer_cache (IMPORTANT)

```sql
id
merchant_id
shopify_customer_id
email
name
phone
address_json
updated_at
```

👉 Used for detection (not real-time API calls)

---

## duplicate_groups

```sql
id
merchant_id
group_hash
customer_ids[]
confidence_score
status (pending, reviewed, merged)
```

---

## merge_records

```sql
id
merchant_id
primary_customer_id
secondary_customer_ids[]
orders_moved
performed_by
created_at
```

---

## snapshots

```sql
id
merchant_id
data JSONB
created_at
```

---

## jobs

```sql
id
type
status
payload JSONB
result JSONB
retries
created_at
```

---

# 🔄 MERGE FLOW (END-TO-END)

This is what your dev team MUST follow.

---

## Step 1: Request

```text
POST /merge/execute
```

---

## Step 2: Create Job

```go
queue.Push("merge_customers", payload)
```

---

## Step 3: Worker Picks Job

```go
processMerge(job)
```

---

## Step 4: Snapshot

```go
snapshotService.Create(customers)
```

---

## Step 5: Validate

```go
validator.Validate(customers)
```

---

## Step 6: Execute

```go
for each order:
  reassign → primary customer
```

---

## Step 7: Merge Metadata

* tags
* notes

---

## Step 8: Archive Secondary

```text
tag: merged_into:PRIMARY_ID
```

---

## Step 9: Audit Log

---

## Step 10: Mark Job Complete

---

# ⚠️ FAILURE HANDLING (DO NOT SKIP)

Every step must:

* Retry (3x)
* Log error
* Stop safely

---

## Example

```go
err := retry(3, func() error {
    return shopify.ReassignOrder(...)
})
```

---

# 🔐 SECURITY + SAFETY

## Required

* Encrypt Shopify tokens
* Validate webhooks (HMAC)
* Role-based access (staff email tracking)

---

# 📡 WEBHOOKS (MANDATORY)

Subscribe to:

* `customers/create`
* `customers/update`
* `customers/delete`

---

## Purpose

* Keep `customer_cache` updated
* Trigger re-detection

---

# ⚙️ INFRA STACK (RECOMMENDED)

| Component | Tool             |
| --------- | ---------------- |
| API       | Go (Fiber / Gin) |
| Queue     | Redis            |
| DB        | PostgreSQL       |
| Hosting   | Fly.io / Railway |
| Logs      | Loki / Datadog   |

---

# ⚠️ FINAL REALITY CHECK (IMPORTANT)

This backend is:

✅ Safe
✅ Scalable
✅ Shopify-compatible

---

But it will FAIL if:

* You skip snapshots
* You skip async jobs
* You trust Shopify API too much

---

# 🎯 What You Can Hand to Dev Team

This is now:

* Clear service boundaries
* Defined data flow
* Exact responsibilities
* No ambiguity