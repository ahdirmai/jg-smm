package repository

import (
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/repository/sqlcgen"
)

// P2 converters. Mirrors the P1 style: Postgres UPPER enums <-> lowercase
// domain enums, pgtype.UUID <-> string, jsonb <-> json.RawMessage.

// --- job enums -------------------------------------------------------------

func jobTypeEnum(t domain.JobType) sqlcgen.JobType {
	return sqlcgen.JobType(upperSnake(string(t)))
}

func jobTypeDomain(t sqlcgen.JobType) domain.JobType {
	return domain.JobType(lowerSnake(string(t)))
}

func jobStatusEnum(s domain.JobStatus) sqlcgen.JobStatus {
	return sqlcgen.JobStatus(upperSnake(string(s)))
}

func jobStatusDomain(s sqlcgen.JobStatus) domain.JobStatus {
	return domain.JobStatus(lowerSnake(string(s)))
}

func upperSnake(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if c == '-' {
			c = '_'
		}
		b = append(b, c)
	}
	return string(b)
}

func lowerSnake(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b = append(b, c)
	}
	return string(b)
}

// --- target ---------------------------------------------------------------

func toTarget(row sqlcgen.Target) domain.Target {
	return domain.Target{
		ID:         uuidString(row.ID),
		Kind:       targetKindDomain(row.Kind),
		Platform:   platformDomain(row.Platform),
		ExternalID: row.ExternalID,
		URL:        row.Url,
		Meta:       json.RawMessage(row.Meta),
		PostID:     uuidStrPtr(row.PostID),
		CommentID:  uuidStrPtr(row.CommentID),
		CreatedAt:  tsTimeOrZero(row.CreatedAt),
	}
}

func targetKindEnum(k domain.TargetKind) sqlcgen.TargetKind {
	return sqlcgen.TargetKind(upperSnake(string(k)))
}

func targetKindDomain(k sqlcgen.TargetKind) domain.TargetKind {
	return domain.TargetKind(lowerSnake(string(k)))
}

// --- post / comment ---------------------------------------------------------

func toPost(row sqlcgen.Post) domain.Post {
	return domain.Post{
		ID:              uuidString(row.ID),
		Platform:        platformDomain(row.Platform),
		ExternalID:      row.ExternalID,
		AuthorHandle:    row.AuthorHandle,
		AuthorID:        row.AuthorID,
		Text:            row.Text,
		MediaURLs:       row.MediaUrls,
		Metrics:         json.RawMessage(row.Metrics),
		ScrapedAt:       tsTimeOrZero(row.ScrapedAt),
		AuthorAccountID: uuidStrPtr(row.AuthorAccountID),
	}
}

func toComment(row sqlcgen.Comment) domain.Comment {
	return domain.Comment{
		ID:           uuidString(row.ID),
		PostID:       uuidString(row.PostID),
		Platform:     platformDomain(row.Platform),
		ExternalID:   row.ExternalID,
		AuthorHandle: row.AuthorHandle,
		Text:         row.Text,
		Metrics:      json.RawMessage(row.Metrics),
		ScrapedAt:    tsTimeOrZero(row.ScrapedAt),
		ParentID:     uuidStrPtr(row.ParentID),
	}
}

// --- scrape job / apify run / raw payload -----------------------------------

func toScrapeJob(row sqlcgen.ScrapeJob) domain.ScrapeJob {
	return domain.ScrapeJob{
		ID:          uuidString(row.ID),
		Type:        jobTypeDomain(row.Type),
		TargetID:    uuidString(row.TargetID),
		AccountID:   uuidStrPtr(row.AccountID),
		WorkerID:    uuidStrPtr(row.WorkerID),
		Status:      jobStatusDomain(row.Status),
		ScheduledAt: tsTimeOrZero(row.ScheduledAt),
		StartedAt:   tsTime(row.StartedAt),
		FinishedAt:  tsTime(row.FinishedAt),
		Attempts:    int(row.Attempts),
		Error:       row.Error,
		CreatedAt:   tsTimeOrZero(row.CreatedAt),
	}
}

func toApifyRun(row sqlcgen.ApifyRun) domain.ApifyRun {
	return domain.ApifyRun{
		ID:          uuidString(row.ID),
		ScrapeJobID: uuidString(row.ScrapeJobID),
		ActorID:     row.ActorID,
		RunID:       row.RunID,
		Status:      row.Status,
		StartedAt:   tsTimeOrZero(row.StartedAt),
		FinishedAt:  tsTime(row.FinishedAt),
	}
}

func toRawPayload(row sqlcgen.RawPayload) domain.RawPayload {
	return domain.RawPayload{
		ID:         uuidString(row.ID),
		ApifyRunID: uuidString(row.ApifyRunID),
		S3Key:      row.S3Key,
		Bytes:      row.Bytes,
		ReceivedAt: tsTimeOrZero(row.ReceivedAt),
	}
}

func toMetricSnapshot(row sqlcgen.MetricSnapshot) domain.MetricSnapshot {
	return domain.MetricSnapshot{
		ID:         uuidString(row.ID),
		PostID:     uuidString(row.PostID),
		TS:         tsTimeOrZero(row.Ts),
		Views:      row.Views,
		Likes:      row.Likes,
		Comments:   row.Comments,
		Shares:     row.Shares,
		Reach:      int64Ptr(row.Reach),
		ReelsViews: int64Ptr(row.ReelsViews),
	}
}

// --- official account analytics --------------------------------------------

func toOfficialAccount(row sqlcgen.OfficialAccount) domain.OfficialAccount {
	return domain.OfficialAccount{
		ID:            uuidString(row.ID),
		Platform:      platformDomain(row.Platform),
		Handle:        row.Handle,
		DisplayName:   row.DisplayName,
		ProfileURL:    row.ProfileUrl,
		AvatarURL:     row.AvatarUrl,
		Status:        officialAccountStatusDomain(row.Status),
		Provider:      analyticsProviderDomain(row.Provider),
		ProviderRef:   row.ProviderRef,
		Tags:          row.Tags,
		LastFetchedAt: tsTime(row.LastFetchedAt),
		CreatedAt:     tsTimeOrZero(row.CreatedAt),
	}
}

func officialAccountStatusEnum(s domain.OfficialAccountStatus) sqlcgen.OfficialAccountStatus {
	return sqlcgen.OfficialAccountStatus(upperSnake(string(s)))
}

func officialAccountStatusDomain(s sqlcgen.OfficialAccountStatus) domain.OfficialAccountStatus {
	return domain.OfficialAccountStatus(lowerSnake(string(s)))
}

func analyticsProviderEnum(p domain.AnalyticsProvider) sqlcgen.AnalyticsProvider {
	return sqlcgen.AnalyticsProvider(upperSnake(string(p)))
}

func analyticsProviderDomain(p sqlcgen.AnalyticsProvider) domain.AnalyticsProvider {
	return domain.AnalyticsProvider(lowerSnake(string(p)))
}

func ingestStatusEnum(s domain.IngestStatus) sqlcgen.IngestStatus {
	return sqlcgen.IngestStatus(upperSnake(string(s)))
}

func ingestStatusDomain(s sqlcgen.IngestStatus) domain.IngestStatus {
	return domain.IngestStatus(lowerSnake(string(s)))
}

func toAnalyticsSnapshot(row sqlcgen.AnalyticsSnapshot) domain.AnalyticsSnapshot {
	return domain.AnalyticsSnapshot{
		ID:                uuidString(row.ID),
		OfficialAccountID: uuidString(row.OfficialAccountID),
		Platform:          platformDomain(row.Platform),
		TS:                tsTimeOrZero(row.Ts),
		Followers:         int64Ptr(row.Followers),
		Reach:             int64Ptr(row.Reach),
		Views:             int64Ptr(row.Views),
		Mentions:          int64Ptr(row.Mentions),
		Engagements:       int64Ptr(row.Engagements),
		ProfileViews:      int64Ptr(row.ProfileViews),
		Metrics:           json.RawMessage(row.Metrics),
		Provider:          analyticsProviderDomain(row.Provider),
		ProviderRunID:     row.ProviderRunID,
		FetchedAt:         tsTimeOrZero(row.FetchedAt),
	}
}

func toAnalyticsMention(row sqlcgen.AnalyticsMention) domain.AnalyticsMention {
	return domain.AnalyticsMention{
		ID:                uuidString(row.ID),
		OfficialAccountID: uuidString(row.OfficialAccountID),
		Platform:          platformDomain(row.Platform),
		ExternalID:        row.ExternalID,
		AuthorHandle:      row.AuthorHandle,
		Text:              row.Text,
		URL:               row.Url,
		PostedAt:          tsTimeOrZero(row.PostedAt),
		Sentiment:         row.Sentiment,
		FetchedAt:         tsTimeOrZero(row.FetchedAt),
	}
}

func toAnalyticsIngestRun(row sqlcgen.AnalyticsIngestRun) domain.AnalyticsIngestRun {
	return domain.AnalyticsIngestRun{
		ID:          uuidString(row.ID),
		Provider:    analyticsProviderDomain(row.Provider),
		Scope:       row.Scope,
		Status:      ingestStatusDomain(row.Status),
		StartedAt:   tsTimeOrZero(row.StartedAt),
		FinishedAt:  tsTime(row.FinishedAt),
		AccountsOk:  int(row.AccountsOk),
		AccountsErr: int(row.AccountsErr),
		ErrorClass:  row.ErrorClass,
		Error:       row.Error,
	}
}

// intervalText renders a Go duration as a Postgres interval literal, used by
// the analytics window queries ($n::interval). Only the magnitudes the
// dashboard needs are supported; anything else is rejected up front by the
// service layer.
func intervalText(d time.Duration) string {
	if d <= 0 {
		d = 24 * time.Hour
	}
	if d%24*time.Hour == 0 {
		days := int64(d / (24 * time.Hour))
		return itoa64(days) + " days"
	}
	return itoa64(int64(d.Hours())) + " hours"
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// metricSnapshotFromAgg builds a MetricSnapshot from a Timescale aggregate row
// (last(x, ts) / max(ts)), whose value columns decode as interface{} because
// they are nullable.
func metricSnapshotFromAgg(postID pgtype.UUID, views, likes, comments, shares, reach, reelsViews, ts interface{}) domain.MetricSnapshot {
	return domain.MetricSnapshot{
		ID:         "", // aggregate rows have no row id
		PostID:     uuidString(postID),
		TS:         derefTimeOrZero(aggTime(ts)),
		Views:      derefInt64OrZero(aggInt64(views)),
		Likes:      derefInt64OrZero(aggInt64(likes)),
		Comments:   derefInt64OrZero(aggInt64(comments)),
		Shares:     derefInt64OrZero(aggInt64(shares)),
		Reach:      aggInt64(reach),
		ReelsViews: aggInt64(reelsViews),
	}
}

func derefInt64OrZero(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefTimeOrZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// jsonbBytes converts a json.RawMessage to the pgtype-free []byte the sqlc
// jsonb override expects, treating a nil message as an empty object.
func jsonbBytes(m json.RawMessage) []byte {
	if len(m) == 0 {
		return []byte("{}")
	}
	return m
}

// int64Ptr / int64Value round-trip nullable bigint columns.
func int64Ptr(p *int64) *int64 {
	return p
}

func int64Value(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// aggInt64 reads a nullable aggregate column. Timescale's `last(x, ts)` and
// `max(ts)` come back as interface{} because the column is nullable; the driver
// decodes them to int64 (or nil for an empty group).
func aggInt64(v interface{}) *int64 {
	switch n := v.(type) {
	case int64:
		return &n
	case *int64:
		return n
	}
	return nil
}

// aggTime reads a `max(ts)`-style aggregate column into *time.Time.
func aggTime(v interface{}) *time.Time {
	switch t := v.(type) {
	case time.Time:
		utc := t.UTC()
		return &utc
	case pgtype.Timestamptz:
		return tsTime(t)
	}
	return nil
}

// pgIntervalArg is the string form passed for a $n::interval bind parameter.
func pgIntervalArg(d time.Duration) string { return intervalText(d) }
