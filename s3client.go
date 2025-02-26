package dziproxylib

import (
	"github.com/aws/aws-sdk-go/aws"
	awscredentials "github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"log"
)

var _s3client *s3.S3

func getS3() *s3.S3 {

	if _s3client != nil {
		return _s3client
	}

	sess, err := session.NewSession(&aws.Config{
		Credentials: awscredentials.NewStaticCredentials(
			LibConfig.S3AccessKey,
			LibConfig.S3SecretKey, "",
		),
		Endpoint:         aws.String(LibConfig.S3Host),
		Region:           aws.String(LibConfig.S3Region),
		DisableSSL:       aws.Bool(!LibConfig.S3UseSSL),
		S3ForcePathStyle: aws.Bool(true),
	})
	if err != nil {
		log.Fatalln(err)
	}
	_s3client = s3.New(sess)
	return _s3client
}
