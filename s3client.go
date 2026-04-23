package dziproxylib

import (
	"log"

	"github.com/aws/aws-sdk-go/aws"
	awscredentials "github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
)

var _s3client *s3.S3

func getS3() *s3.S3 {

	if _s3client != nil {
		return _s3client
	}

	s, err := session.NewSession(&aws.Config{
		Credentials: awscredentials.NewStaticCredentials(
			LibConfig.S3AccessKey,
			LibConfig.S3SecretKey, "",
		),
		Endpoint:         new(LibConfig.S3Host),
		Region:           new(LibConfig.S3Region),
		DisableSSL:       new(!LibConfig.S3UseSSL),
		S3ForcePathStyle: new(true),
	})
	if err != nil {
		log.Fatalln(err)
	}
	_s3client = s3.New(s)
	return _s3client
}
