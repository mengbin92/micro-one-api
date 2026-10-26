package main

import (
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/app/identity/internal/data"
	"micro-one-api/app/identity/internal/server"
)

func emailAuthenticationPolicy(cfg *Config, repo *data.Repository) server.RegistrationPolicy {
	policy := registrationPolicyFromConfig(cfg)
	policy.CodeDeliverer = data.NewCodeDelivererFromEnv()
	policy.Challenges = biz.NewEmailChallengeUsecase(data.NewChallengeRepo(repo.Data), policy.CodeDeliverer)
	return policy
}
