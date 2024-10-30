package tempo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/golang/protobuf/jsonpb"
	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/tracing"
	"github.com/grafana/grafana/pkg/tsdb/tempo/kinds/dataquery"
	"github.com/grafana/grafana/pkg/tsdb/tempo/traceql"
	"github.com/grafana/tempo/pkg/tempopb"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func (s *Service) runTraceQlQuery(ctx context.Context, pCtx backend.PluginContext, query backend.DataQuery) (*backend.DataResponse, error) {
	ctxLogger := s.logger.FromContext(ctx)
	ctxLogger.Debug("Running TraceQL query", "function", logEntrypoint())

	ctx, span := tracing.DefaultTracer().Start(ctx, "datasource.tempo.runTraceQLQuery", trace.WithAttributes(
		attribute.String("queryType", query.QueryType),
	))
	defer span.End()

	result := &backend.DataResponse{}

	model := &dataquery.TempoQuery{}
	err := json.Unmarshal(query.JSON, model)
	if err != nil {
		ctxLogger.Error("Failed to unmarshall Tempo query model", "error", err, "function", logEntrypoint())
		return result, err
	}

	dsInfo, err := s.getDSInfo(ctx, pCtx)
	if err != nil {
		ctxLogger.Error("Failed to get datasource information", "error", err, "function", logEntrypoint())
		return nil, err
	}

	if model.Query == nil || *model.Query == "" {
		err := fmt.Errorf("query is required")
		ctxLogger.Error("Failed to validate model query", "error", err, "function", logEntrypoint())
		return result, err
	}

	resp, responseBody, err := s.performMetricsQuery(ctx, dsInfo, model, query, span)
	if err != nil {
		return result, err
	}

	if resp.StatusCode != http.StatusOK {
		ctxLogger.Error("Failed to execute TraceQL query", "error", err, "function", logEntrypoint())
		result.Error = fmt.Errorf("failed to execute TraceQL query: %s Status: %s Body: %s", *model.Query, resp.Status, string(responseBody))
		span.RecordError(result.Error)
		span.SetStatus(codes.Error, result.Error.Error())
		return result, nil
	}

	var queryResponse tempopb.QueryRangeResponse
	err = jsonpb.Unmarshal(bytes.NewReader(responseBody), &queryResponse)

	if err != nil {
		ctxLogger.Error("Failed to convert response to type", "error", err, "function", logEntrypoint())
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return &backend.DataResponse{}, fmt.Errorf("failed to convert response to type: %w", err)
	}

	frames := traceql.TransformMetricsResponse(queryResponse)

	result.Frames = frames
	ctxLogger.Debug("Successfully performed TraceQL query", "function", logEntrypoint())
	return result, nil
}

func (s *Service) performMetricsQuery(ctx context.Context, dsInfo *Datasource, model *dataquery.TempoQuery, query backend.DataQuery, span trace.Span) (*http.Response, []byte, error) {
	ctxLogger := s.logger.FromContext(ctx)
	request, err := s.createMetricsQuery(ctx, dsInfo, model, query.TimeRange.From.Unix(), query.TimeRange.To.Unix())

	if err != nil {
		ctxLogger.Error("Failed to create request", "error", err, "function", logEntrypoint())
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, nil, err
	}

	resp, err := dsInfo.HTTPClient.Do(request)
	if err != nil {
		ctxLogger.Error("Failed to send request to Tempo", "error", err, "function", logEntrypoint())
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, nil, fmt.Errorf("failed get to tempo: %w", err)
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			ctxLogger.Error("Failed to close response body", "error", err, "function", logEntrypoint())
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		ctxLogger.Error("Failed to read response body", "error", err, "function", logEntrypoint())
		return nil, nil, err
	}
	return resp, body, nil
}

func (s *Service) createMetricsQuery(ctx context.Context, dsInfo *Datasource, query *dataquery.TempoQuery, start int64, end int64) (*http.Request, error) {
	ctxLogger := s.logger.FromContext(ctx)

	rawUrl := fmt.Sprintf("%s/api/metrics/query_range", dsInfo.URL)
	searchUrl, err := url.Parse(rawUrl)
	if err != nil {
		ctxLogger.Error("Failed to parse URL", "url", rawUrl, "error", err, "function", logEntrypoint())
	}

	q := searchUrl.Query()
	q.Set("q", *query.Query)
	q.Set("start", strconv.FormatInt(start, 10))
	q.Set("end", strconv.FormatInt(end, 10))
	if query.Step != nil {
		q.Set("step", *query.Step)
	}

	searchUrl.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", searchUrl.String(), nil)
	if err != nil {
		ctxLogger.Error("Failed to create request", "error", err, "function", logEntrypoint())
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	return req, nil
}
