package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	v "micro-one-api/api/identity/v1"
)

func rescueIAMCredential(endpoint string, uid int64, password, reason string, userRevision, policyRevision uint64) error {
	token := os.Getenv("IAM_RESCUE_SERVICE_TOKEN")
	credential := os.Getenv("ADMIN_TOKEN")
	if strings.TrimSpace(endpoint) == "" || token == "" || credential == "" || uid <= 0 || userRevision == 0 || policyRevision == 0 || strings.TrimSpace(reason) == "" || len(password) < 8 {
		return fmt.Errorf("IAM rescue requires endpoint, target ID, reason, both revisions, ADMIN_TOKEN and IAM_RESCUE_SERVICE_TOKEN")
	}
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token, "x-operator-authorization", "Bearer "+credential))
	_, err = v.NewIAMServiceClient(conn).RescueRootCredential(ctx, &v.IAMRescueRequest{UserId: uid, Password: password, Reason: reason, ExpectedRevision: userRevision, ExpectedPolicyRevision: policyRevision})
	return err
}
