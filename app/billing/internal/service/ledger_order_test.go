package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNormalizeLedgerOrderAllowsOnlyMappedColumns(t *testing.T) {
	column, direction, err := normalizeLedgerOrder("amount DESC")
	require.NoError(t, err)
	require.Equal(t, "amount", column)
	require.Equal(t, "desc", direction)

	_, _, err = normalizeLedgerOrder("amount desc; drop table billing_ledgers")
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, _, err = normalizeLedgerOrder("unknown asc")
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
