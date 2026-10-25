package metrics

import "github.com/prometheus/client_golang/prometheus"

var VendorReportMonitor = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "micro_one_api", Subsystem: "billing", Name: "vendor_report_monitor", Help: "Bounded latest report coverage by finite state; unknown is not money."}, []string{"state"})

var VendorReportNextScanDeadline = prometheus.NewGauge(prometheus.GaugeOpts{Namespace: "micro_one_api", Subsystem: "billing", Name: "vendor_report_next_scan_deadline_seconds", Help: "Unix deadline for the next supplier evidence monitor scan, including five minutes of scheduling grace."})

func init() { prometheus.MustRegister(VendorReportMonitor, VendorReportNextScanDeadline) }
