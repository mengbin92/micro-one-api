package server

import (
	"context"
	"io"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/internal/adaptor"
	"net/http"
)

func convertRequestObserved(ctx context.Context, ad adaptor.Adaptor, rc *adaptor.RelayContext, format adaptor.Format, body []byte) (out adaptor.Format, data []byte, err error) {
	_, end := requesttrace.Phase(ctx, "request.transform")
	defer func() { end(err) }()
	return ad.ConvertRequest(rc, format, body)
}
func convertResponseObserved(ctx context.Context, ad adaptor.Adaptor, rc *adaptor.RelayContext, format adaptor.Format, response *http.Response) (out adaptor.Format, data []byte, err error) {
	_, end := requesttrace.Phase(ctx, "response.transform")
	defer func() { end(err) }()
	return ad.ConvertResponse(rc, format, response)
}
func convertStreamObserved(ctx context.Context, ad adaptor.Adaptor, rc *adaptor.RelayContext, format adaptor.Format, response *http.Response) (out adaptor.Format, reader io.Reader, err error) {
	_, end := requesttrace.Phase(ctx, "response.transform")
	defer func() { end(err) }()
	return ad.ConvertStreamResponse(rc, format, response)
}
