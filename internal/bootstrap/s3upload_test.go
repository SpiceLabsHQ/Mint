package bootstrap

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeS3Client implements mintaws.S3BucketAPI for testing.
type fakeS3Client struct {
	headBucketErr        error // nil means bucket exists
	createBucketErr      error
	putObjectErr         error
	putPublicAccessErr   error

	headBucketCalls        int
	createBucketCalls      int
	putObjectCalls         int
	putPublicAccessCalls   int

	lastCreateInput  *s3.CreateBucketInput
	lastPutObjKey    string
}

func (f *fakeS3Client) HeadBucket(_ context.Context, params *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	f.headBucketCalls++
	if f.headBucketErr != nil {
		return nil, f.headBucketErr
	}
	return &s3.HeadBucketOutput{}, nil
}

func (f *fakeS3Client) CreateBucket(_ context.Context, params *s3.CreateBucketInput, _ ...func(*s3.Options)) (*s3.CreateBucketOutput, error) {
	f.createBucketCalls++
	f.lastCreateInput = params
	if f.createBucketErr != nil {
		return nil, f.createBucketErr
	}
	return &s3.CreateBucketOutput{}, nil
}

func (f *fakeS3Client) PutObject(_ context.Context, params *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.putObjectCalls++
	if params.Key != nil {
		f.lastPutObjKey = *params.Key
	}
	if f.putObjectErr != nil {
		return nil, f.putObjectErr
	}
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3Client) PutPublicAccessBlock(_ context.Context, _ *s3.PutPublicAccessBlockInput, _ ...func(*s3.Options)) (*s3.PutPublicAccessBlockOutput, error) {
	f.putPublicAccessCalls++
	if f.putPublicAccessErr != nil {
		return nil, f.putPublicAccessErr
	}
	return &s3.PutPublicAccessBlockOutput{}, nil
}

// fakePresigner implements mintaws.PresignGetObjectAPI for testing.
type fakePresigner struct {
	url string
	err error
}

func (f *fakePresigner) PresignGetObject(_ context.Context, _ *s3.GetObjectInput, _ ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &v4.PresignedHTTPRequest{URL: f.url}, nil
}

// noSuchBucketError returns an error that errors.As can unwrap to *s3types.NoSuchBucket.
func noSuchBucketError() error {
	return &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 404}},
		Err:      &s3types.NoSuchBucket{Message: aws.String("no such bucket")},
	}
}

// ---------------------------------------------------------------------------
// BucketName
// ---------------------------------------------------------------------------

func TestBucketName(t *testing.T) {
	tests := []struct {
		accountID string
		region    string
		want      string
	}{
		{"123456789012", "us-west-2", "mint-bootstrap-123456789012-us-west-2"},
		{"000000000000", "eu-central-1", "mint-bootstrap-000000000000-eu-central-1"},
		{"123456789012", "us-east-1", "mint-bootstrap-123456789012-us-east-1"},
	}
	for _, tc := range tests {
		got := BucketName(tc.accountID, tc.region)
		if got != tc.want {
			t.Errorf("BucketName(%q, %q) = %q; want %q", tc.accountID, tc.region, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// UploadAndPresign happy path
// ---------------------------------------------------------------------------

func TestUploadAndPresign_HappyPath_BucketExists(t *testing.T) {
	const wantURL = "https://s3.example.com/presigned"
	s3c := &fakeS3Client{} // HeadBucket succeeds → bucket exists
	presigner := &fakePresigner{url: wantURL}

	sha256 := "abc123"
	url, err := UploadAndPresign(context.Background(), s3c, presigner,
		"us-west-2", "123456789012", []byte("#!/bin/bash\necho hello"), sha256)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != wantURL {
		t.Errorf("URL = %q; want %q", url, wantURL)
	}
	if s3c.headBucketCalls != 1 {
		t.Errorf("HeadBucket called %d times; want 1", s3c.headBucketCalls)
	}
	if s3c.createBucketCalls != 0 {
		t.Errorf("CreateBucket called %d times; want 0 (bucket exists)", s3c.createBucketCalls)
	}
	if s3c.putObjectCalls != 1 {
		t.Errorf("PutObject called %d times; want 1", s3c.putObjectCalls)
	}
	wantKey := "bootstrap/abc123/bootstrap.sh"
	if s3c.lastPutObjKey != wantKey {
		t.Errorf("PutObject key = %q; want %q", s3c.lastPutObjKey, wantKey)
	}
}

// ---------------------------------------------------------------------------
// Bucket auto-creation
// ---------------------------------------------------------------------------

func TestUploadAndPresign_BucketNotFound_Creates(t *testing.T) {
	s3c := &fakeS3Client{headBucketErr: noSuchBucketError()}
	presigner := &fakePresigner{url: "https://presigned-url"}

	_, err := UploadAndPresign(context.Background(), s3c, presigner,
		"us-west-2", "111122223333", []byte("script"), "deadbeef")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s3c.createBucketCalls != 1 {
		t.Errorf("CreateBucket calls = %d; want 1", s3c.createBucketCalls)
	}
	if s3c.putPublicAccessCalls != 1 {
		t.Errorf("PutPublicAccessBlock calls = %d; want 1", s3c.putPublicAccessCalls)
	}
	if s3c.putObjectCalls != 1 {
		t.Errorf("PutObject calls = %d; want 1", s3c.putObjectCalls)
	}
}

// ---------------------------------------------------------------------------
// us-east-1 special case: no LocationConstraint
// ---------------------------------------------------------------------------

func TestUploadAndPresign_UsEast1_NoLocationConstraint(t *testing.T) {
	s3c := &fakeS3Client{headBucketErr: noSuchBucketError()}
	presigner := &fakePresigner{url: "https://presigned"}

	_, err := UploadAndPresign(context.Background(), s3c, presigner,
		"us-east-1", "123456789012", []byte("script"), "sha256abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s3c.lastCreateInput == nil {
		t.Fatal("CreateBucket was not called")
	}
	if s3c.lastCreateInput.CreateBucketConfiguration != nil {
		t.Errorf("us-east-1 CreateBucket must have nil CreateBucketConfiguration; got %+v",
			s3c.lastCreateInput.CreateBucketConfiguration)
	}
}

// ---------------------------------------------------------------------------
// Other region: must include LocationConstraint
// ---------------------------------------------------------------------------

func TestUploadAndPresign_OtherRegion_HasLocationConstraint(t *testing.T) {
	s3c := &fakeS3Client{headBucketErr: noSuchBucketError()}
	presigner := &fakePresigner{url: "https://presigned"}

	_, err := UploadAndPresign(context.Background(), s3c, presigner,
		"eu-west-1", "123456789012", []byte("script"), "sha256xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s3c.lastCreateInput == nil {
		t.Fatal("CreateBucket was not called")
	}
	if s3c.lastCreateInput.CreateBucketConfiguration == nil {
		t.Fatal("non-us-east-1 CreateBucket must include CreateBucketConfiguration")
	}
	want := s3types.BucketLocationConstraint("eu-west-1")
	got := s3c.lastCreateInput.CreateBucketConfiguration.LocationConstraint
	if got != want {
		t.Errorf("LocationConstraint = %q; want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// HeadBucket error that is NOT NoSuchBucket
// ---------------------------------------------------------------------------

func TestUploadAndPresign_HeadBucketUnknownError(t *testing.T) {
	s3c := &fakeS3Client{headBucketErr: errors.New("forbidden")}
	presigner := &fakePresigner{url: "https://presigned"}

	_, err := UploadAndPresign(context.Background(), s3c, presigner,
		"us-west-2", "123456789012", []byte("script"), "sha")
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	if s3c.createBucketCalls != 0 {
		t.Errorf("CreateBucket should not be called on non-404 HeadBucket error")
	}
}

// ---------------------------------------------------------------------------
// PutObject failure
// ---------------------------------------------------------------------------

func TestUploadAndPresign_PutObjectError(t *testing.T) {
	s3c := &fakeS3Client{putObjectErr: errors.New("upload failed")}
	presigner := &fakePresigner{url: "https://presigned"}

	_, err := UploadAndPresign(context.Background(), s3c, presigner,
		"us-west-2", "123456789012", []byte("script"), "sha")
	if err == nil {
		t.Fatal("expected error; got nil")
	}
}

// ---------------------------------------------------------------------------
// Presign failure
// ---------------------------------------------------------------------------

func TestUploadAndPresign_PresignError(t *testing.T) {
	s3c := &fakeS3Client{}
	presigner := &fakePresigner{err: errors.New("presign failed")}

	_, err := UploadAndPresign(context.Background(), s3c, presigner,
		"us-west-2", "123456789012", []byte("script"), "sha")
	if err == nil {
		t.Fatal("expected error; got nil")
	}
}
