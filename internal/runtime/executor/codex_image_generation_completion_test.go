package executor

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestCodexExecutorExecuteStream_CompletesImageGenerationItemsWhenResultPresent(t *testing.T) {
	// Given
	const imageResult = "iVBORw0KGgo="
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"ig_123","type":"image_generation_call","status":"generating","result":"` + imageResult + `"}}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[{"id":"ig_123","type":"image_generation_call","status":"generating","result":"` + imageResult + `"}]}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "test",
		"base_url": server.URL,
	}}

	// When
	stream, errExecute := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5.6-sol",
		Payload: []byte(`{"model":"gpt-5.6-sol","input":"draw a cat"}`),
	}, cliproxyexecutor.Options{
		SourceFormat:   sdktranslator.FromString("openai-response"),
		ResponseFormat: sdktranslator.FromString("openai-response"),
		Stream:         true,
	})
	if errExecute != nil {
		t.Fatalf("ExecuteStream() error = %v", errExecute)
	}

	var outputItemDone, completed []byte
	for chunk := range stream.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream chunk error = %v", chunk.Err)
		}
		payload := bytes.TrimSpace(chunk.Payload)
		if !bytes.HasPrefix(payload, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(payload[5:])
		switch gjson.GetBytes(data, "type").String() {
		case "response.output_item.done":
			outputItemDone = bytes.Clone(data)
		case "response.completed":
			completed = bytes.Clone(data)
		}
	}

	// Then
	if got := gjson.GetBytes(outputItemDone, "item.status").String(); got != "completed" {
		t.Fatalf("output_item.done item.status = %q, want completed; event=%s", got, outputItemDone)
	}
	if got := gjson.GetBytes(completed, "response.output.0.status").String(); got != "completed" {
		t.Fatalf("response.completed output status = %q, want completed; event=%s", got, completed)
	}
	if got := gjson.GetBytes(outputItemDone, "item.result").String(); got != imageResult {
		t.Fatalf("output_item.done item.result = %q, want unchanged result", got)
	}
	if got := gjson.GetBytes(completed, "response.output.0.result").String(); got != imageResult {
		t.Fatalf("response.completed output result = %q, want unchanged result", got)
	}
}

func TestCodexExecutorExecute_CompletesCollectedImageGenerationItemWhenFinalOutputEmpty(t *testing.T) {
	// Given
	const imageResult = "iVBORw0KGgo="
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"ig_123","type":"image_generation_call","status":"generating","result":"` + imageResult + `"}}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[]}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"api_key":  "test",
		"base_url": server.URL,
	}}

	// When
	response, errExecute := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5.6-sol",
		Payload: []byte(`{"model":"gpt-5.6-sol","input":"draw a cat"}`),
	}, cliproxyexecutor.Options{
		SourceFormat:   sdktranslator.FromString("openai-response"),
		ResponseFormat: sdktranslator.FromString("openai-response"),
	})

	// Then
	if errExecute != nil {
		t.Fatalf("Execute() error = %v", errExecute)
	}
	if got := gjson.GetBytes(response.Payload, "output.0.status").String(); got != "completed" {
		t.Fatalf("output[0].status = %q, want completed; payload=%s", got, response.Payload)
	}
	if got := gjson.GetBytes(response.Payload, "output.0.result").String(); got != imageResult {
		t.Fatalf("output[0].result = %q, want unchanged result", got)
	}
}
