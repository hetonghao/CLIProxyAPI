package helps

import (
	"bytes"
	"compress/gzip"
)

const (
	// DevinMaxRequestBodyBytes is the request body limit enforced by the Devin upstream gateway (nginx 10m).
	DevinMaxRequestBodyBytes = 10 << 20
	// devinRequestBodyBudget leaves headroom for payload rules applied after image fitting.
	devinRequestBodyBudget = DevinMaxRequestBodyBytes - 256<<10
	// devinCompressThreshold is the proto size above which request frames are gzip-compressed.
	devinCompressThreshold = 1 << 20
	// DevinOmittedImagePlaceholder replaces history images dropped to fit the request limit.
	DevinOmittedImagePlaceholder = "[Earlier image omitted to fit the Devin request size limit]"
)

// WrapDevinRequestBody frames a GetChatMessage payload, gzip-compressing large payloads
// so they fit the upstream gateway body limit. Devin accepts gzip Connect request frames.
func WrapDevinRequestBody(protoBytes []byte) (body []byte, compressed bool) {
	if len(protoBytes) <= devinCompressThreshold {
		return WrapConnectEnvelope(protoBytes), false
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(protoBytes); err != nil {
		return WrapConnectEnvelope(protoBytes), false
	}
	if err := gz.Close(); err != nil {
		return WrapConnectEnvelope(protoBytes), false
	}
	return WrapConnectEnvelopeWithFlag(ConnectFlagCompressed, buf.Bytes()), true
}

// FitDevinImagesToRequestLimit replaces the oldest prompt images with text placeholders until
// the framed request produced by build fits the Devin gateway limit. The newest images are kept.
// It returns the final payload, the prompts used to build it, and the number of omitted images.
func FitDevinImagesToRequestLimit(prompts []DevinPrompt, build func([]DevinPrompt) []byte) ([]byte, []DevinPrompt, int) {
	protoBytes := build(prompts)
	body, _ := WrapDevinRequestBody(protoBytes)
	if len(body) <= devinRequestBodyBudget {
		return protoBytes, prompts, 0
	}

	type imageRef struct{ prompt, image int }
	var refs []imageRef
	for p := range prompts {
		for i := range prompts[p].Images {
			refs = append(refs, imageRef{p, i})
		}
	}

	fitted := prompts
	omitted := 0
	for len(body) > devinRequestBodyBudget && omitted < len(refs) {
		// Base64 image data compresses to roughly its decoded size, so estimate savings as 3/4.
		excess := len(body) - devinRequestBodyBudget
		for saved := 0; omitted < len(refs) && saved < excess; omitted++ {
			ref := refs[omitted]
			saved += len(prompts[ref.prompt].Images[ref.image].Base64Data) * 3 / 4
		}

		dropped := make(map[imageRef]bool, omitted)
		for _, ref := range refs[:omitted] {
			dropped[ref] = true
		}
		fitted = make([]DevinPrompt, len(prompts))
		for p, prompt := range prompts {
			kept := make([]DevinImage, 0, len(prompt.Images))
			for i, img := range prompt.Images {
				if !dropped[imageRef{p, i}] {
					kept = append(kept, img)
					continue
				}
				if prompt.Content != "" {
					prompt.Content += "\n"
				}
				prompt.Content += DevinOmittedImagePlaceholder
			}
			prompt.Images = kept
			fitted[p] = prompt
		}
		protoBytes = build(fitted)
		body, _ = WrapDevinRequestBody(protoBytes)
	}
	return protoBytes, fitted, omitted
}
