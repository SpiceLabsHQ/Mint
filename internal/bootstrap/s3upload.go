package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	mintaws "github.com/nicholasgasior/mint/internal/aws"
)

// bootstrapPresignTTL is how long the presigned GET URL remains valid.
// Bootstrap takes at most ~15 minutes; 2 hours provides ample headroom.
const bootstrapPresignTTL = 2 * time.Hour

// BucketName returns the S3 bucket name for bootstrap script delivery,
// derived from the AWS account ID and region so it is unique per account/region.
// Format: mint-bootstrap-{accountID}-{region}
func BucketName(accountID, region string) string {
	return fmt.Sprintf("mint-bootstrap-%s-%s", accountID, region)
}

// UploadAndPresign uploads bootstrap.sh to S3 and returns a presigned GET URL
// valid for 2 hours. It auto-creates the bucket if it does not exist and
// blocks all public access on newly-created buckets.
//
// Parameters:
//   - ctx:       request context
//   - s3Client:  S3 client for bucket management and object upload
//   - presigner: S3 presign client for generating the GET URL
//   - region:    AWS region where the bucket should live
//   - accountID: AWS account ID (extracted from owner ARN)
//   - content:   bootstrap.sh bytes to upload
//   - sha256:    hex SHA256 of content; used as key prefix for idempotency
//
// Returns a presigned URL valid for bootstrapPresignTTL (2 hours).
func UploadAndPresign(
	ctx context.Context,
	s3Client mintaws.S3BucketAPI,
	presigner mintaws.PresignGetObjectAPI,
	region, accountID string,
	content []byte,
	sha256 string,
) (string, error) {
	bucket := BucketName(accountID, region)

	if err := ensureBucket(ctx, s3Client, bucket, region); err != nil {
		return "", fmt.Errorf("ensure S3 bucket %q: %w", bucket, err)
	}

	key := fmt.Sprintf("bootstrap/%s/bootstrap.sh", sha256)
	contentType := "text/x-shellscript"
	_, err := s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(content),
		ContentLength: aws.Int64(int64(len(content))),
		ContentType:   aws.String(contentType),
	})
	if err != nil {
		return "", fmt.Errorf("upload bootstrap.sh to s3://%s/%s: %w", bucket, key, err)
	}

	req, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(bootstrapPresignTTL))
	if err != nil {
		return "", fmt.Errorf("presign bootstrap.sh GET URL: %w", err)
	}

	return req.URL, nil
}

// ensureBucket checks whether bucket exists; creates it if not.
// Applies public-access-block on newly created buckets for security hygiene.
func ensureBucket(ctx context.Context, client mintaws.S3BucketAPI, bucket, region string) error {
	_, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucket),
	})
	if err == nil {
		// Bucket already exists — nothing to do.
		return nil
	}

	// Check whether the error is a "bucket does not exist" (404) response.
	var noSuchBucket *s3types.NoSuchBucket
	if !errors.As(err, &noSuchBucket) {
		// Some other error (permissions, network, etc.) — propagate it.
		return fmt.Errorf("head bucket: %w", err)
	}

	// Create the bucket. us-east-1 is the AWS special case: it must NOT include
	// a CreateBucketConfiguration (specifying LocationConstraint for us-east-1
	// returns an InvalidLocationConstraint error).
	createInput := &s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	}
	if region != "us-east-1" {
		createInput.CreateBucketConfiguration = &s3types.CreateBucketConfiguration{
			LocationConstraint: s3types.BucketLocationConstraint(region),
		}
	}

	if _, err := client.CreateBucket(ctx, createInput); err != nil {
		return fmt.Errorf("create bucket %q: %w", bucket, err)
	}

	// Block all public access on the newly-created bucket. This is a
	// security-hygiene measure: the bootstrap script is not secret, but
	// there is no reason to allow public access to this bucket. Fail the
	// bucket creation if this call fails, since an unprotected bucket is
	// not acceptable.
	t := true
	if _, err := client.PutPublicAccessBlock(ctx, &s3.PutPublicAccessBlockInput{
		Bucket: aws.String(bucket),
		PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{
			BlockPublicAcls:       &t,
			BlockPublicPolicy:     &t,
			IgnorePublicAcls:      &t,
			RestrictPublicBuckets: &t,
		},
	}); err != nil {
		return fmt.Errorf("put public access block on %q: %w", bucket, err)
	}

	return nil
}
