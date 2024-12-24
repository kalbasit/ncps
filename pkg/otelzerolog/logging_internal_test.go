package otelzerolog

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/log"
)

func TestGetKeyValueForMap(t *testing.T) {
	t.Parallel()

	t.Run("map include a bool", func(t *testing.T) {
		t.Parallel()

		assert.Equal(
			t,
			[]log.KeyValue{log.Bool("a", true)},
			getKeyValueForMap(map[string]interface{}{"a": true}),
		)
	})

	t.Run("map includes a string", func(t *testing.T) {
		t.Parallel()

		assert.Equal(
			t,
			[]log.KeyValue{
				log.String("a", "test"),
			},
			getKeyValueForMap(map[string]interface{}{
				"a": "test",
			}),
		)
	})

	t.Run("map includes a float64", func(t *testing.T) {
		t.Parallel()

		assert.Equal(
			t,
			[]log.KeyValue{
				log.Float64("a", 10.5),
			},
			getKeyValueForMap(map[string]interface{}{
				"a": 10.5,
			}),
		)
	})

	t.Run("map includes a slice", func(t *testing.T) {
		t.Parallel()

		kvs := getKeyValueForMap(map[string]interface{}{
			"a": []interface{}{"b"},
		})

		if assert.Len(t, kvs, 1) {
			assert.True(t, kvs[0].Equal(
				log.Slice(
					"a",
					log.StringValue("b"),
				),
			))
		}
	})

	t.Run("map includes a map", func(t *testing.T) {
		t.Parallel()

		kvs := getKeyValueForMap(map[string]interface{}{
			"a": map[string]interface{}{
				"b": "c",
			},
		})

		if assert.Len(t, kvs, 1) {
			assert.True(t, kvs[0].Equal(
				log.Map(
					"a",
					log.String("b", "c"),
				),
			))
		}
	})
}

func TestGetValuesForSlice(t *testing.T) {
	t.Parallel()

	t.Run("list of bool", func(t *testing.T) {
		t.Parallel()

		assert.Equal(
			t,
			[]log.Value{
				log.BoolValue(true),
				log.BoolValue(false),
			},
			getValuesForSlice([]interface{}{true, false}),
		)
	})

	t.Run("list of float64", func(t *testing.T) {
		t.Parallel()

		assert.Equal(
			t,
			[]log.Value{
				log.Float64Value(10.5),
				log.Float64Value(20.5),
			},
			getValuesForSlice([]interface{}{10.5, 20.5}),
		)
	})

	t.Run("list of strings", func(t *testing.T) {
		t.Parallel()

		assert.Equal(
			t,
			[]log.Value{
				log.StringValue("a"),
				log.StringValue("b"),
			},
			getValuesForSlice([]interface{}{"a", "b"}),
		)
	})

	t.Run("list of maps", func(t *testing.T) {
		t.Parallel()

		assert.Equal(
			t,
			[]log.Value{
				log.MapValue(
					log.String("a", "c"),
				),
				log.MapValue(
					log.Bool("b", true),
				),
			},
			getValuesForSlice([]interface{}{
				map[string]interface{}{"a": "c"},
				map[string]interface{}{"b": true},
			}),
		)
	})
}
