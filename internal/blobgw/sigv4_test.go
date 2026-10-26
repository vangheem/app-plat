package blobgw

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/minio/minio-go/v7/pkg/signer"
)

func TestVerifyMatchesMinioSigner(t *testing.T) {
	body := []byte("hello from golang")
	req, err := http.NewRequest(http.MethodPut, "http://blob-gateway:9000/app-golang-demo/demo.txt", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "text/plain")
	req.ContentLength = int64(len(body))
	req.Host = "blob-gateway:9000"
	signed := signer.SignV4(*req, "bk_test", "secret-value", "", "us-east-1")
	raw, _ := io.ReadAll(signed.Body)
	if !verify(signed, raw, "bk_test", "secret-value", "us-east-1") {
		t.Fatalf("expected signature to verify, auth=%s sha=%s", signed.Header.Get("Authorization"), signed.Header.Get("X-Amz-Content-Sha256"))
	}
	if verify(signed, raw, "bk_test", "wrong", "us-east-1") {
		t.Fatal("wrong secret should fail")
	}
}
