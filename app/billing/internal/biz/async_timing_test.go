package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type panicTimingReservationRepo struct{ ReservationRepo }

func (*panicTimingReservationRepo) GetReservation(context.Context, string) (*Reservation, error) {
	panic("storage panic")
}

func TestSettlementWorkerPanicRecordsFailedSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		require.NoError(t, provider.Shutdown(context.Background()))
	})

	uc := &AsyncBillingUsecase{
		workerCtx: context.Background(),
		syncUc:    NewBillingUsecase(nil, &panicTimingReservationRepo{}, nil, nil, nil),
	}
	require.NotPanics(t, func() {
		uc.processSettlement(&SettleTask{ReservationID: "panic-reservation", Timestamp: time.Now()})
	})
	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	require.Equal(t, "settlement.worker", spans[0].Name)
	for _, attr := range spans[0].Attributes {
		if attr.Key == "outcome" {
			require.Equal(t, "failed", attr.Value.AsString())
			return
		}
	}
	t.Fatal("settlement worker did not record an outcome")
}
