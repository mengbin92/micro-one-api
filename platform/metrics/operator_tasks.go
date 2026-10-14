package metrics

import "github.com/prometheus/client_golang/prometheus"

var OperatorTaskStates = prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "micro_one_api", Name: "operator_tasks", Help: "Bounded operator invocation states by fixed role; uncertain calls are not retried or counted as zero cost."}, []string{"kind", "state"})

func init() { prometheus.MustRegister(OperatorTaskStates) }
