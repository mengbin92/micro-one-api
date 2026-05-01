package auth

type ContextKey string

const (
	ContextKeyUserID  ContextKey = "user_id"
	ContextKeyTokenID ContextKey = "token_id"
	ContextKeyGroup   ContextKey = "group"
)
