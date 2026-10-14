package metrics

import "github.com/prometheus/client_golang/prometheus"

var VendorReportMonitor = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "micro_one_api", Subsystem: "billing", Name: "vendor_report_monitor", Help: "Bounded latest report coverage by finite state; unknown is not money."}, []string{"state"})

func init() { prometheus.MustRegister(VendorReportMonitor) }
