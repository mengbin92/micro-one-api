package routing

// Group is the channel-owned group identity shared as a pure value across
// application boundaries. It does not contain prices or user entitlements.
type Group struct {
	ID              int64
	Key             string
	DisplayName     string
	Description     string
	Status          string
	AccessMode      string
	ModelAccessMode string
	SortOrder       int32
	Revision        int64
	CreatedAt       int64
	UpdatedAt       int64
}

type GroupResource struct {
	Source   Source
	Priority int64
	Weight   int64
}

type GroupModelGrant struct {
	MappingID          int64
	AccountID          int64
	Model              string
	UpstreamModelID    string
	Enabled            bool
	Priority           int32
	ExtraAuthorization bool
}

type GroupDetail struct {
	Group       *Group
	Resources   []GroupResource
	ModelGrants []GroupModelGrant
}

type GroupListRequest struct {
	PageSize  int32
	PageToken string
	Filter    string
	OrderBy   string
}
type GroupListResult struct {
	Groups        []*Group
	NextPageToken string
}
