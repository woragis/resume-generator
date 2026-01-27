# TODO: Resume-generator — AI enrichment & validation hardening

## What happened

- AI enrichment returned values violating strict JSON Schema (short publication strings, wrong `extras` type).
- Processor hard-merged those fields, validation failed, job fell back to base resume (missing publications/certs/extras).
- Later job timed out calling `ai-service` (context deadline exceeded).

---

## Immediate fixes (High Priority)

### Timeout & Retry

- [ ] - Increase AI client timeout in `server/pkg/ai/client.go` (e.g., 20s → 60s).
- [ ] - Add 2-attempt retry with exponential backoff for `ai-service` calls.
- [ ] - Surface clearer error messages in logs for transient failures.

### Validation & Type Coercion

- [ ] - Ensure `EnrichFields`/`EnrichResume` outputs are normalized before merging:
  - [ ] - Coerce `publications`/`certifications` to arrays of strings.
  - [ ] - Ensure `extras` is always a string (handle array/null cases).
  - [ ] - Expand short `publications` locally to meet `minLength` (40+ chars).
- [ ] - Add defensive type coercion before calling `model.ValidateMap` to prevent type errors.

### Testing

- [ ] - Run integration test (`cmd/test_processor`) to validate end-to-end flow locally.
- [ ] - Trigger a real job in compose stack after fixes to confirm publications/certs/extras render.

---

## Medium-term improvements

### Local Formatting (avoid LLM roundtrips)

- [ ] - Implement local deterministic formatting for `publications` (compose "Title — YEAR. Summary" pattern).
- [ ] - Implement local formatting for `certifications` if not provided by AI.
- [ ] - Keep AI for high-value transformations (summary, experience bullets) only.

### Schema & Prompts

- [ ] - Relax or make schema constraints configurable (e.g., publication `minLength`).
- [ ] - Harden AI prompts to explicitly require types and example JSON output.
- [ ] - Document AI prompt design decisions and constraints.

### Test Coverage

- [ ] - Expand `cmd/test_processor` to cover edge cases (empty lists, short strings, type mismatches).
- [ ] - Add parametrized test cases for various AI response formats.

---

## Long-term / Operational improvements

### Observability

- [ ] - Add metrics for AI latency, validation failures, and job success/failure rates.
- [ ] - Add tracing for AI request/response lifecycle.
- [ ] - Add alerts for repeated AI timeouts or validation error spikes.

### Deployment & Rollout

- [ ] - Consider feature-flagging LLM-driven enrichment vs local-only formatting.
- [ ] - Add health checks for ai-service availability.
- [ ] - Document fallback behaviors for ai-service outages.

---

## Recommended next actions (practical order)

1. **Immediate deploy (this sprint)**
   - [ ] - Increase AI timeout to 60s + add 2-attempt retry (small code change).
   - [ ] - Redeploy and trigger a real job to validate timeout fix resolves transient failures.

2. **This sprint**
   - [ ] - Keep `processor` normalization patch; extend local publication expansion logic.
   - [ ] - Add defensive type coercion before validation.
   - [ ] - Run integration tests locally.

3. **Next sprint**
   - [ ] - Implement local formatting for publications/certifications.
   - [ ] - Expand test coverage.
   - [ ] - Add monitoring and alerts.

4. **Long term**
   - [ ] - Relax schema constraints carefully and document rationale.
   - [ ] - Feature-flag LLM enrichment for safer rollout.

---

## Files touched during investigation

- `server/internal/usecase/processor.go` (override normalization, enrichment hard-merge, expansion logic)
- `server/pkg/ai/client.go` (timeout, EnrichFields method, logging)
- `server/templates/style.css` (CSS inlining verification)
- `server/cmd/test_processor/main.go` (integration test harness)
