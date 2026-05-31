// NHN Cloud Key Manager(Secure Key Manager) 대칭키 복호화 클라이언트.
// (TS의 lib/kms-nhn 대응) POST .../symmetric-keys/{keyId}/decrypt
package kms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const defaultNHNBaseURL = "https://api-keymanager.nhncloudservice.com/keymanager/v1.0"

// NHNClient 는 NHN KMS Decryptor 구현체.
type NHNClient struct {
	appKey  string
	baseURL string
	http    *http.Client
}

// NewNHNClient 생성. baseURL 미지정 시 기본값. appKey 필수.
func NewNHNClient(appKey, baseURL string) (*NHNClient, error) {
	if appKey == "" {
		return nil, fmt.Errorf("NHN KMS: appKey required (NHN_APPKEY)")
	}
	if baseURL == "" {
		baseURL = defaultNHNBaseURL
	}
	return &NHNClient{
		appKey:  appKey,
		baseURL: baseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}, nil
}

type nhnDecryptResponse struct {
	Header struct {
		ResultCode    int    `json:"resultCode"`
		ResultMessage string `json:"resultMessage"`
		IsSuccessful  bool   `json:"isSuccessful"`
	} `json:"header"`
	Body struct {
		Plaintext string `json:"plaintext"`
	} `json:"body"`
}

// Decrypt 는 keyId 대칭키로 ciphertextBase64를 복호화해 평문을 반환한다.
func (c *NHNClient) Decrypt(ctx context.Context, keyID, ciphertextBase64 string) (string, error) {
	endpoint := fmt.Sprintf("%s/appkey/%s/symmetric-keys/%s/decrypt",
		c.baseURL, c.appKey, url.PathEscape(keyID))

	payload, err := json.Marshal(map[string]string{"ciphertext": ciphertextBase64})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var data nhnDecryptResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", fmt.Errorf("NHN KMS decrypt: decode response: %w", err)
	}
	if resp.StatusCode/100 != 2 || !data.Header.IsSuccessful {
		msg := data.Header.ResultMessage
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("NHN KMS decrypt: %s", msg)
	}
	if data.Body.Plaintext == "" {
		return "", fmt.Errorf("NHN KMS decrypt: empty plaintext in response")
	}
	return data.Body.Plaintext, nil
}
