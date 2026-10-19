module github.com/yousysadmin/whoosh/plugins/aws

go 1.27.0

require (
	github.com/aws/aws-sdk-go-v2 v1.47.3
	github.com/aws/aws-sdk-go-v2/config v1.33.9
	github.com/aws/aws-sdk-go-v2/credentials v1.20.9
	github.com/aws/aws-sdk-go-v2/service/autoscaling v1.78.4
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.339.0
	github.com/aws/aws-sdk-go-v2/service/secretsmanager v1.50.4
	github.com/aws/aws-sdk-go-v2/service/ssm v1.79.3
	github.com/aws/smithy-go v1.28.5
	github.com/yousysadmin/whoosh v1.8.1
	go.yaml.in/yaml/v3 v3.0.5
)

require (
	github.com/Masterminds/semver/v3 v3.5.0 // indirect
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.20.3 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.6 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.6 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.5.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.14.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.10.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.38.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.43.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.51.4 // indirect
	github.com/gliderlabs/ssh v0.3.8 // indirect
	golang.org/x/crypto v0.56.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
)

replace github.com/yousysadmin/whoosh => ../..
