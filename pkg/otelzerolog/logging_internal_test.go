package otelzerolog

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/attribute"
)

func TestGetKeyValueForMap(t *testing.T) {
	t.Parallel()

	t.Run("map include a bool", func(t *testing.T) {
		t.Parallel()

		assert.Equal(
			t,
			[]attribute.KeyValue{attribute.Bool("a", true)},
			getKeyValueForMap(map[string]any{"a": true}),
		)
	})

	t.Run("map includes a string", func(t *testing.T) {
		t.Parallel()

		assert.Equal(
			t,
			[]attribute.KeyValue{
				attribute.String("a", "test"),
			},
			getKeyValueForMap(map[string]any{
				"a": "test",
			}),
		)
	})

	t.Run("map includes a float64", func(t *testing.T) {
		t.Parallel()

		assert.Equal(
			t,
			[]attribute.KeyValue{
				attribute.Float64("a", 10.5),
			},
			getKeyValueForMap(map[string]any{
				"a": 10.5,
			}),
		)
	})

	t.Run("map includes a slice", func(t *testing.T) {
		t.Parallel()

		kvs := getKeyValueForMap(map[string]any{
			"a": []any{"b"},
		})

		if assert.Len(t, kvs, 1) {
			assert.Equal(
				t,
				attribute.Slice(
					"a",
					attribute.StringValue("b"),
				),
				kvs[0],
			)
		}
	})

	t.Run("map includes a map", func(t *testing.T) {
		t.Parallel()

		kvs := getKeyValueForMap(map[string]any{
			"a": map[string]any{
				"b": "c",
			},
		})

		if assert.Len(t, kvs, 1) {
			assert.Equal(
				t,
				attribute.Map(
					"a",
					attribute.String("b", "c"),
				),
				kvs[0],
			)
		}
	})
}

func TestGetValuesForSlice(t *testing.T) {
	t.Parallel()

	t.Run("list of bool", func(t *testing.T) {
		t.Parallel()

		assert.Equal(
			t,
			[]attribute.Value{
				attribute.BoolValue(true),
				attribute.BoolValue(false),
			},
			getValuesForSlice([]any{true, false}),
		)
	})

	t.Run("list of float64", func(t *testing.T) {
		t.Parallel()

		assert.Equal(
			t,
			[]attribute.Value{
				attribute.Float64Value(10.5),
				attribute.Float64Value(20.5),
			},
			getValuesForSlice([]any{10.5, 20.5}),
		)
	})

	t.Run("list of strings", func(t *testing.T) {
		t.Parallel()

		assert.Equal(
			t,
			[]attribute.Value{
				attribute.StringValue("a"),
				attribute.StringValue("b"),
			},
			getValuesForSlice([]any{"a", "b"}),
		)
	})

	t.Run("list of maps", func(t *testing.T) {
		t.Parallel()

		assert.Equal(
			t,
			[]attribute.Value{
				attribute.MapValue(
					attribute.String("a", "c"),
				),
				attribute.MapValue(
					attribute.Bool("b", true),
				),
			},
			getValuesForSlice([]any{
				map[string]any{"a": "c"},
				map[string]any{"b": true},
			}),
		)
	})
}
