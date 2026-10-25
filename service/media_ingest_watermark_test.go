package service

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/mediastore"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

type capturedPersist struct {
	key         string
	data        []byte
	contentType string
	meta        map[string]string
}

func useWatermarkSettings(t *testing.T, enabled bool, imageBinding string) {
	t.Helper()
	settings := system_setting.GetWatermarkSettings()
	previous := *settings
	previous.Templates = append([]system_setting.WatermarkTemplate(nil), settings.Templates...)
	previous.GroupBindings = make(map[string]system_setting.WatermarkBinding, len(settings.GroupBindings))
	for group, binding := range settings.GroupBindings {
		previous.GroupBindings[group] = binding
	}
	settings.Enabled = enabled
	settings.Templates = []system_setting.WatermarkTemplate{{
		Name: "test", Text: "AI生成", Position: "bottom_right",
		FontScale: 0.04, Opacity: 0.6, Color: "#FFFFFF",
	}}
	settings.DefaultBinding = system_setting.WatermarkBinding{Image: imageBinding, Video: "none"}
	settings.GroupBindings = map[string]system_setting.WatermarkBinding{}
	t.Cleanup(func() { *settings = previous })
}

func stubWatermarkIngest(t *testing.T, persisted *[]capturedPersist, failures *[]error, applyErr error) {
	t.Helper()
	oldPersist, oldApply, oldAvailable, oldRecord := persistObject, applyWatermark, watermarkAvailable, recordWatermarkFailure
	persistObject = func(_ context.Context, key string, source mediastore.PersistSource, meta map[string]string) error {
		var data []byte
		switch {
		case source.LocalPath != "":
			read, err := os.ReadFile(source.LocalPath)
			if err != nil {
				return err
			}
			data = read
		case len(source.Data) > 0:
			data = append([]byte(nil), source.Data...)
		default:
			data = []byte(source.UpstreamURL + source.NFSPath)
		}
		copyMeta := make(map[string]string, len(meta))
		for k, v := range meta {
			copyMeta[k] = v
		}
		*persisted = append(*persisted, capturedPersist{
			key: key, data: data, contentType: source.ContentType, meta: copyMeta,
		})
		return nil
	}
	watermarkAvailable = func() (bool, string) { return true, "" }
	applyWatermark = func(_ context.Context, inPath, outPath string, _ system_setting.MediaKind, _ string, _ system_setting.WatermarkTemplate) error {
		if applyErr != nil {
			return applyErr
		}
		data, err := os.ReadFile(inPath)
		if err != nil {
			return err
		}
		return os.WriteFile(outPath, append(data, []byte("::watermarked::")...), 0o600)
	}
	recordWatermarkFailure = func(_, _, _ string, _ int, _, _ string, err error) {
		*failures = append(*failures, err)
	}
	t.Cleanup(func() {
		persistObject, applyWatermark, watermarkAvailable, recordWatermarkFailure = oldPersist, oldApply, oldAvailable, oldRecord
	})
}

func TestPersistMediaWatermarkUnmatchedPersistsOriginalOnce(t *testing.T) {
	useWatermarkSettings(t, true, "none")
	var persisted []capturedPersist
	var failures []error
	stubWatermarkIngest(t, &persisted, &failures, nil)
	raw := []byte("original")
	if err := persistMedia(context.Background(), "result.png", mediastore.PersistSource{Data: raw}, nil, watermarkTarget{
		Group: "other", Source: "test", KindHint: system_setting.MediaKindImage,
	}); err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 || persisted[0].key != "result.png" || string(persisted[0].data) != string(raw) {
		t.Fatalf("persisted = %+v, want one unchanged object", persisted)
	}
	if len(failures) != 0 {
		t.Fatalf("failures = %v, want none", failures)
	}
}

func TestPersistMediaWatermarkStoresOriginalAndRendered(t *testing.T) {
	useWatermarkSettings(t, true, "test")
	var persisted []capturedPersist
	var failures []error
	stubWatermarkIngest(t, &persisted, &failures, nil)
	raw := []byte("original bytes")
	meta := map[string]string{"user-id": "42"}
	if err := persistMedia(context.Background(), "result.png", mediastore.PersistSource{Data: raw}, meta, watermarkTarget{
		Group: "default", Source: "test", KindHint: system_setting.MediaKindImage,
	}); err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 2 {
		t.Fatalf("persist calls = %d, want original and watermarked", len(persisted))
	}
	if persisted[0].key != "result.orig.png" || string(persisted[0].data) != string(raw) {
		t.Fatalf("original upload = %+v", persisted[0])
	}
	if persisted[0].meta["watermark"] != "" {
		t.Fatalf("original metadata contains watermark marker: %+v", persisted[0].meta)
	}
	if persisted[1].key != "result.png" || string(persisted[1].data) == string(raw) {
		t.Fatalf("watermarked upload = %+v", persisted[1])
	}
	if persisted[1].meta["watermark"] != "1" || persisted[1].contentType != "image/png" {
		t.Fatalf("watermarked metadata/content type = %+v, %q", persisted[1].meta, persisted[1].contentType)
	}
	if len(failures) != 0 {
		t.Fatalf("failures = %v, want none", failures)
	}
}

func TestPersistMediaApplyFailureFallsBackToOriginal(t *testing.T) {
	useWatermarkSettings(t, true, "test")
	var persisted []capturedPersist
	var failures []error
	stubWatermarkIngest(t, &persisted, &failures, errors.New("render failed"))
	raw := []byte("original bytes")
	if err := persistMedia(context.Background(), "result.png", mediastore.PersistSource{Data: raw}, nil, watermarkTarget{
		Group: "default", Source: "task", UserId: 42, TaskId: "task-1",
		KindHint: system_setting.MediaKindImage,
	}); err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 || persisted[0].key != "result.png" || string(persisted[0].data) != string(raw) {
		t.Fatalf("fallback upload = %+v, want original at requested key", persisted)
	}
	if len(failures) != 1 || failures[0].Error() != "render failed" {
		t.Fatalf("recorded failures = %v, want render failure", failures)
	}
}

func TestPersistMediaWatermarkDisabledPersistsOriginalOnce(t *testing.T) {
	useWatermarkSettings(t, false, "test")
	var persisted []capturedPersist
	var failures []error
	stubWatermarkIngest(t, &persisted, &failures, nil)
	raw := []byte("original")
	if err := persistMedia(context.Background(), "result.png", mediastore.PersistSource{Data: raw}, nil, watermarkTarget{
		Group: "default", Source: "test", KindHint: system_setting.MediaKindImage,
	}); err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 || string(persisted[0].data) != string(raw) {
		t.Fatalf("persisted = %+v, want one unchanged object", persisted)
	}
	if len(failures) != 0 {
		t.Fatalf("failures = %v, want none", failures)
	}
}

func TestRewriteImageB64WatermarkSuccessDoesNotPersist(t *testing.T) {
	useWatermarkSettings(t, true, "test")
	mediaSettings := system_setting.GetMediaStorageSettings()
	previousEnabled := mediaSettings.Enabled
	mediaSettings.Enabled = true
	t.Cleanup(func() { mediaSettings.Enabled = previousEnabled })
	var persisted []capturedPersist
	var failures []error
	stubWatermarkIngest(t, &persisted, &failures, nil)
	source := map[string]any{"data": []map[string]string{{"b64_json": "b3JpZ2luYWw="}}}
	body, err := common.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	out, keys := RewriteImageResponseToOBS(context.Background(), 42, 0, "", "image-model", "b64_json", body)
	var result struct {
		Data []struct {
			B64 string `json:"b64_json"`
		} `json:"data"`
	}
	if err := common.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 1 || result.Data[0].B64 == "b3JpZ2luYWw=" {
		t.Fatalf("b64_json was not replaced: %+v", result.Data)
	}
	if len(keys) != 0 || len(persisted) != 0 || len(failures) != 0 {
		t.Fatalf("keys=%v persisted=%+v failures=%v; b64 path must be in-memory only", keys, persisted, failures)
	}
}

func TestRewriteImageB64WatermarkFailureKeepsOriginal(t *testing.T) {
	useWatermarkSettings(t, true, "test")
	mediaSettings := system_setting.GetMediaStorageSettings()
	previousEnabled := mediaSettings.Enabled
	mediaSettings.Enabled = true
	t.Cleanup(func() { mediaSettings.Enabled = previousEnabled })
	var persisted []capturedPersist
	var failures []error
	stubWatermarkIngest(t, &persisted, &failures, errors.New("render failed"))
	body := []byte(`{"data":[{"b64_json":"b3JpZ2luYWw="}]}`)
	out, keys := RewriteImageResponseToOBS(context.Background(), 42, 0, "", "image-model", "b64_json", body)
	if string(out) != string(body) {
		t.Fatalf("failed watermark changed response: %s", out)
	}
	if len(keys) != 0 || len(persisted) != 0 || len(failures) != 1 {
		t.Fatalf("keys=%v persisted=%+v failures=%v", keys, persisted, failures)
	}
}
