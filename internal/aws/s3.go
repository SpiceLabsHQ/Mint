// Package aws provides thin wrappers around AWS SDK clients used by Mint.
// This file defines narrow interfaces for S3 operations needed by the
// bootstrap S3 upload and presign workflow.
package aws

import (
	"context"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// PutObjectAPI defines the subset of the S3 API used for uploading bootstrap.sh.
type PutObjectAPI interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// HeadBucketAPI defines the subset used to check bucket existence.
type HeadBucketAPI interface {
	HeadBucket(ctx context.Context, params *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
}

// CreateBucketAPI defines the subset used to create a bucket.
type CreateBucketAPI interface {
	CreateBucket(ctx context.Context, params *s3.CreateBucketInput, optFns ...func(*s3.Options)) (*s3.CreateBucketOutput, error)
}

// PutPublicAccessBlockAPI defines the subset used to block public access.
type PutPublicAccessBlockAPI interface {
	PutPublicAccessBlock(ctx context.Context, params *s3.PutPublicAccessBlockInput, optFns ...func(*s3.Options)) (*s3.PutPublicAccessBlockOutput, error)
}

// PresignGetObjectAPI defines the subset used to presign S3 GET requests.
// The return type is *v4.PresignedHTTPRequest (from aws/signer/v4), which is
// what s3.PresignClient.PresignGetObject returns.
type PresignGetObjectAPI interface {
	PresignGetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

// S3BucketAPI groups all S3 bucket management operations needed for bootstrap
// script delivery into a single interface for mock injection in tests.
type S3BucketAPI interface {
	PutObjectAPI
	HeadBucketAPI
	CreateBucketAPI
	PutPublicAccessBlockAPI
}

// Compile-time checks: *s3.Client satisfies all narrow interfaces.
var (
	_ PutObjectAPI            = (*s3.Client)(nil)
	_ HeadBucketAPI           = (*s3.Client)(nil)
	_ CreateBucketAPI         = (*s3.Client)(nil)
	_ PutPublicAccessBlockAPI = (*s3.Client)(nil)
	_ PresignGetObjectAPI     = (*s3.PresignClient)(nil)
	_ S3BucketAPI             = (*s3.Client)(nil)
)
