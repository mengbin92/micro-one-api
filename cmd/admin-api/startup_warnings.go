package main

import (
	"os"

	"github.com/go-kratos/kratos/v2/log"
)

// warnIfLogDeleteUnconfigured emits a startup warning when the prerequisites
// for admin log-service proxy actions are missing. The routes stay registered
// and return NotImplemented at request time, but operators should know up front
// that log detail and cleanup actions are inert until LOG_HTTP_ENDPOINT and
// SERVICE_TOKEN are set.
func warnIfLogDeleteUnconfigured(helper *log.Helper) {
	logEndpoint := os.Getenv("LOG_HTTP_ENDPOINT")
	serviceToken := os.Getenv("SERVICE_TOKEN")
	if logEndpoint != "" && serviceToken != "" {
		return
	}
	missing := []string{}
	if logEndpoint == "" {
		missing = append(missing, "LOG_HTTP_ENDPOINT")
	}
	if serviceToken == "" {
		missing = append(missing, "SERVICE_TOKEN")
	}
	helper.Warnf("log service proxy disabled: missing %v; /api/log/{id} detail and /api/log/ DELETE cleanup will return 501 until these are set (see docs/deployment.md §4.3)", missing)
}
