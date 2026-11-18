package registry

import (
	"reflect"
	"testing"
)

func TestDevinGPT62ModelsSurviveCatalogRefresh(t *testing.T) {
	original := GetDevinModelsJSON()
	t.Cleanup(func() {
		if _, err := loadDevinModelsFromBytes(original, "restore"); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := loadDevinModelsFromBytes([]byte(`{"devin":[{"id":"gpt-6-sol","object":"model","type":"devin"}]}`), "test"); err != nil {
		t.Fatal(err)
	}
	for _, family := range []string{"astra", "sol"} {
		t.Run(family, func(t *testing.T) {
			id := "devin/gpt-6-2-" + family
			count := 0
			for _, model := range GetDevinModels() {
				if model.ID == id {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("catalog contains %d copies of %s, want 1", count, id)
			}
			for _, lookupID := range []string{id, "gpt-6-2-" + family} {
				model := LookupDevinModel(lookupID)
				if model == nil {
					t.Fatalf("%s missing after catalog refresh", lookupID)
				}
				if model.ContextLength != 1050000 || model.InputTokenLimit != 1050000 ||
					model.MaxCompletionTokens != 64000 || model.OutputTokenLimit != 64000 ||
					model.OwnedBy != "openai" || model.Type != "devin" ||
					!reflect.DeepEqual(model.SupportedInputModalities, []string{"text", "image"}) ||
					!reflect.DeepEqual(model.SupportedOutputModalities, []string{"text"}) ||
					model.Thinking == nil || !reflect.DeepEqual(model.Thinking.Levels, []string{"low", "medium", "high", "xhigh", "max"}) {
					t.Fatalf("unexpected GPT-6.2 metadata: %+v", model)
				}
			}
		})
	}
}

func TestWithDevinBuiltinsPrefersUpstreamGPT62Models(t *testing.T) {
	for _, family := range []string{"astra", "sol"} {
		t.Run(family, func(t *testing.T) {
			upstream := &ModelInfo{ID: "devin/gpt-6-2-" + family, ContextLength: 2000000, Thinking: &ThinkingSupport{Levels: []string{"high"}}}
			count := 0
			for _, model := range WithDevinBuiltins([]*ModelInfo{upstream}) {
				if model.ID == upstream.ID {
					count++
					if !reflect.DeepEqual(model, upstream) {
						t.Fatalf("upstream metadata replaced: %+v", model)
					}
				}
			}
			if count != 1 {
				t.Fatalf("model count = %d, want 1", count)
			}
		})
	}
}
