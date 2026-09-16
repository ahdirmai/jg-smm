package obs

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics is the process-wide Prometheus instrument set. One registry, one
// factory: the handlers that increment these are spread across the service and
// http layers, and a metric that is declared per-package silently becomes a
// second metric with the same name when a second package does the same thing.
// Holding them here keeps the names unique and the registration testable.
type Metrics struct {
	// registry is what /metrics exposes. Default registry is used so the
	// process and runtime collectors Go ships are included for free.
	registry *prometheus.Registry

	Actions        *prometheus.CounterVec
	ActionDuration *prometheus.HistogramVec
	WorkerHealth   *prometheus.GaugeVec
	Workers        *prometheus.GaugeVec
	Accounts       *prometheus.GaugeVec
	QueueDepth     *prometheus.GaugeVec

	// P5-08 cost metrics. Apify is billed per run and proxy per byte; both are
	// money, and a budget alert needs a counter that only ever goes up.
	ApifyRunCostUSD *prometheus.CounterVec
	ProxyBytes      *prometheus.CounterVec
	// BudgetCeiling is the monthly spend limit, published as a gauge so the
	// 90% budget alert divides a real budget rather than a hardcoded number in
	// the rule file. A ceiling that lives only in the alert expression is one
	// that gets raised instead of respected.
	BudgetCeiling *prometheus.GaugeVec
}

// NewMetrics registers the instrument set on the default registry. Labels are
// the closed sets the dashboards group by: adding a label here is a dashboard
// change, not a local one, so they are deliberately coarse.
func NewMetrics() *Metrics {
	f := promauto.With(prometheus.DefaultRegisterer)

	m := &Metrics{
		registry: prometheus.DefaultRegisterer.(*prometheus.Registry),
		Actions: f.NewCounterVec(prometheus.CounterOpts{
			Namespace: "smm",
			Name:      "action_total",
			Help:      "Actions attempted, by account, platform, type and outcome. The success rate is the primary fleet health signal.",
		}, []string{"account_id", "platform", "action_type", "status", "error_class"}),

		ActionDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "smm",
			Name:      "action_duration_seconds",
			Help:      "Wall time of one action attempt, from dispatch to verdict. P5-07's p95 target is measured here.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"platform", "action_type", "status"}),

		WorkerHealth: f.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "smm",
			Name:      "worker_health_score",
			Help:      "Account health score 0-100 (P5-02). Below 30 the account is quarantined; a fleet-wide slide means the platform is throttling us, not the accounts.",
		}, []string{"account_id", "platform"}),

		Workers: f.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "smm",
			Name:      "workers_total",
			Help:      "Worker containers by reported status. crashloop is the flapping state P5-02 quarantines on.",
		}, []string{"status", "region"}),

		Accounts: f.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "smm",
			Name:      "accounts_total",
			Help:      "Worker accounts by lifecycle status and platform. quarantined rising is the alert that pairs with worker_health_score falling.",
		}, []string{"status", "platform"}),

		QueueDepth: f.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "smm",
			Name:      "queue_depth",
			Help:      "Pending action jobs per account. Depth that only falls when actions fail is a ban-risk signal, not throughput.",
		}, []string{"account_id", "platform"}),

		ApifyRunCostUSD: f.NewCounterVec(prometheus.CounterOpts{
			Namespace: "smm",
			Name:      "apify_run_cost_usd_total",
			Help:      "Apify spend in USD. A budget alert alerts on 90% of the monthly ceiling; this counter is the only accurate source of cumulative spend.",
		}, []string{"actor"}),

		ProxyBytes: f.NewCounterVec(prometheus.CounterOpts{
			Namespace: "smm",
			Name:      "proxy_bytes_used_total",
			Help:      "Proxy egress in bytes. Residential pools bill per byte, so this is the second half of the cost budget.",
		}, []string{"proxy_group"}),

		BudgetCeiling: f.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "smm",
			Name:      "budget_ceiling",
			Help:      "Monthly spend ceiling per budget. The 90% alert divides this rather than a constant in the rule file.",
		}, []string{"budget"}),
	}

	return m
}

// Registry returns the registry /metrics exposes.
func (m *Metrics) Registry() *prometheus.Registry { return m.registry }
