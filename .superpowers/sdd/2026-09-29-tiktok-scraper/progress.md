# SDD ledger — plan: docs/superpowers/plans/2026-09-29-tiktok-scraper.md

Setup: inline execution chosen by operator. Branch: main (the operator's
established pattern this session; 87e4bab and 216db4d are already on it).
No spec file reachable on disk — the actor input/output schema was pasted into
the conversation and is reproduced in the plan's Global Constraints and each
task's fixtures. Rulings made without an on-disk spec are provisional.

Pre-flight interface scan (3 shared rows, 0 conflicts):
- T1 -> T4: T1 produces `parseTikTokItems(raw []byte, window port.KeywordTimeWindow)
  ([]scrapeItem, error)`; T4's `TestScrapeKeywordsValidates` rewrite and T1's own
  `keywordRun` reach tests need the platform gate open. Found: the plan already
  sequences this and tells T1 to accept those tests failing on the gate.
- T2 -> T4: T2 produces a `domain.PlatformTikTok` branch in
  `(*Runner).searchInput(in port.ApifySearchInput, maxItems int) map[string]any`;
  T4 does not name it. Found: no conflict.
- T3 -> T5: T3 produces `APIFY_SEARCH_ACTOR_TIKTOK` in compose.yaml and
  `docs/platforms/tiktok.md`; T5 reads both. Found: no conflict.

Task 1: complete (commits 216db4d..$(git rev-parse --short HEAD), tests: go test ./internal/service/ -run TestParseTikTok -> 8/8 pass; go test ./... -> all packages ok)
Task 1: Ruling: authorMeta.id typed `any`, not `string` — the actor emits it as a number and a string field fails the whole row's unmarshal. Cost if wrong: one field type, caught by TestParseTikTokNestedAuthorAndMetrics.
Task 1: Ruling: the plan's fixture used createTime=1785001611, which is 2026-07-25 — outside the test window. TestParseTikTokFallsBackToUnixCreateTime failed for that reason, not because the parser was wrong. Fixture now derives createTime from createTimeISO so the two date fields can never disagree. Cost if wrong: none; a disagreeing fixture turns a date regression into an unrelated "row vanished" failure.
Task 2: complete (commits 9399ebd..$(git rev-parse --short HEAD), tests: go test ./internal/adapter/apify/ -> 10/10 pass; go test ./... -> all packages ok)
Task 2: Ruling: the two guard tests (OmitsChargeableAddons, OmitsZeroWindow) passed BEFORE the branch existed — vacuously, because the TikTok payload fell through to the Instagram branch which also sends neither key. They are real assertions only after this task. Worth remembering: a guard test that passes pre-implementation proves nothing until the code under it exists.
Task 3: complete (commits 6e21e12..$(git rev-parse --short HEAD), tests: docker compose config -> APIFY_SEARCH_ACTOR_TIKTOK: clockworks/tiktok-scraper resolved in api env)
Task 3: Ruling: actor pinned by NAME (clockworks/tiktok-scraper) in compose.yaml, not by the numeric id GdWCkxBtKWOsKjdch as the plan said. The three existing actors use store names, and the id GdWCkxBtKWOsKjdch in the pasted brief is the same form of opaque id the plan elsewhere calls "the full id" — which for these actors is the namespace/name slug. The rule being protected is "never derive from APIFY_ACTOR_PREFIX", and a name satisfies it identically. Cost if wrong: one config value.
Task 4: complete (commits 3c76bd1..$(git rev-parse --short HEAD), tests: go test ./... -> all packages ok; tsc --noEmit -> exit 0)
Task 4: Ruling: domain.MVPPlatforms NOT extended with TikTok, contrary to the plan. It is the packer's quota/licensing input (packer_test.go "mvp_2_platforms_quota_2": 2 platforms x maxPer 2 = 50 containers), not a list of implemented scrapers. Adding a platform changes quota math across the fleet — a product decision, not an implementation one, and not needed for this feature. Cost if wrong: TikTok is absent from whatever MVP_PLATFORMS gates; the keyword-batch path uses keywordScrapable() instead and works.
Task 4: Ruling: the FE keyword-batch picker needed changing (it hardcoded 'instagram' | 'threads'), which the plan did not mention. Without it the feature is unreachable from the dashboard and the whole task is dead code. Cost if wrong: none.
Task 4: Ruling: TestScrapeKeywordsValidates's "no actor for platform" case was testing the test helper's contents, not the rule — it relied on Threads having no actor in keywordSvcFor, which stopped being true once the helper got a Threads actor. Rewritten to build a service with an empty resolver. Cost if wrong: none; the rule is unchanged.
Final review: 2 Critical, 2 Important, 3 Minor. All fixed in f8f6f9e + 2 follow-ups.
Review Ruling: the feature was DEAD ON ARRIVAL. config.Config had no
ApifySearchActorTikTok and searchActorForPlatform never returned one, so
APIFY_SEARCH_ACTOR_TIKTOK was read by no Go code and every TikTok batch 400'd
with "no search actor configured" before a single Apify call. Every
actor-resolution test used a FAKE resolver that answered TikTok correctly; only
cmd/server's resolver is production. Lesson: a fake injected at the seam cannot
detect a field that is missing at the factory. main_test.go now drives off the
scrapable platform list so a platform added to the gate without an actor fails on
the same commit.
Review Ruling: playCount was stored as a new `plays` key. The snapshot writer
reads metricsInt("views","viewCount","playCount") — an unread key is stored and
read by nobody, i.e. a stored view count of ZERO on an otherwise perfect row, and
TopPostsByMetric orders by metric_snapshot.views so such a post can never rank.
Cost if wrong: silent zero on every ranking, no error anywhere.
Review Ruling: parseActorTime ACCEPTS fractional seconds via RFC3339. A "fix the
test" pass that used "…07:06:51.123456" to prove the unparseable-date path proved
nothing — it parsed. Use "08/03/2026 07:06:51", which is rejected by every layout
tried. Cost if wrong: a test that passes for the wrong reason.
Review Ruling: webVideoUrl was parsed into a struct field and dropped — TikTok
posts had no permalink at all and the batch page dead-ended. Derived from
authorHandle + externalId like IG/Threads; field deleted rather than kept.
Review Ruling: image sequencing. The web image was built before the permalink
fix landed, so the deployed bundle still returned null for TikTok while the code
on disk was correct. Verified by reading the SERVED bundle, not the source.
Lesson: after a web-side fix, grep the served _next chunk for the new string.
Only a source-level check would have said it was shipped when it was not.
Task 5: NOT run. Requires operator's explicit go-ahead (real money) and a
chosen keyword. Nothing in this plan has touched a paid Apify run.
