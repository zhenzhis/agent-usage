package integrations

import (
	"encoding/hex"
	"fmt"

	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// DecodeOTelProtoTraceSpans decodes an OTLP ExportTraceServiceRequest protobuf
// into the same compact span representation used by the JSON receiver.
func DecodeOTelProtoTraceSpans(raw []byte) ([]OTelSpan, error) {
	var req collectortracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("decode OTLP protobuf traces: %w", err)
	}
	out := []OTelSpan{}
	for _, rs := range req.GetResourceSpans() {
		resourceAttrs := protoAttributes(rs.GetResource().GetAttributes())
		for _, ss := range rs.GetScopeSpans() {
			scopeName := ""
			if ss.GetScope() != nil {
				scopeName = ss.GetScope().GetName()
			}
			for _, span := range ss.GetSpans() {
				out = append(out, otelSpanFromProto(span, resourceAttrs, scopeName))
			}
		}
	}
	return out, nil
}

func otelSpanFromProto(span *tracepb.Span, resourceAttrs map[string]interface{}, scopeName string) OTelSpan {
	return OTelSpan{
		TraceID:          protoID(span.GetTraceId()),
		SpanID:           protoID(span.GetSpanId()),
		ParentSpanID:     protoID(span.GetParentSpanId()),
		Name:             span.GetName(),
		StartTime:        unixNano(int64(span.GetStartTimeUnixNano())),
		EndTime:          unixNano(int64(span.GetEndTimeUnixNano())),
		Attributes:       protoAttributes(span.GetAttributes()),
		ResourceAttrs:    cloneAttrs(resourceAttrs),
		Instrumentation:  scopeName,
		SourceConvention: "opentelemetry.otlp_protobuf",
	}
}

func protoAttributes(attrs []*commonpb.KeyValue) map[string]interface{} {
	out := map[string]interface{}{}
	for _, attr := range attrs {
		if attr == nil || attr.GetKey() == "" {
			continue
		}
		out[attr.GetKey()] = protoAnyValue(attr.GetValue())
	}
	return out
}

func protoAnyValue(value *commonpb.AnyValue) interface{} {
	if value == nil {
		return nil
	}
	switch typed := value.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return typed.StringValue
	case *commonpb.AnyValue_BoolValue:
		return typed.BoolValue
	case *commonpb.AnyValue_IntValue:
		return typed.IntValue
	case *commonpb.AnyValue_DoubleValue:
		return typed.DoubleValue
	case *commonpb.AnyValue_ArrayValue:
		values := typed.ArrayValue.GetValues()
		out := make([]interface{}, 0, len(values))
		for _, item := range values {
			out = append(out, protoAnyValue(item))
		}
		return out
	case *commonpb.AnyValue_KvlistValue:
		return protoAttributes(typed.KvlistValue.GetValues())
	case *commonpb.AnyValue_BytesValue:
		return fmt.Sprintf("<bytes:%d>", len(typed.BytesValue))
	default:
		return nil
	}
}

func protoID(value []byte) string {
	if len(value) == 0 {
		return ""
	}
	return hex.EncodeToString(value)
}
