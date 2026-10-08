module gopkg.aoctech.app/wallet/pix-gateway

go 1.27

require (
	github.com/aws/aws-lambda-go v1.55.1
	github.com/aws/aws-sdk-go-v2 v1.47.1
	github.com/aws/aws-sdk-go-v2/config v1.33.7
	github.com/aws/aws-sdk-go-v2/service/ssm v1.79.1
	github.com/caarlos0/env/v11 v11.4.1
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
	gopkg.aoctech.app/api-commons v1.13.1
	gopkg.aoctech.app/wallet/rpc-contract v0.0.0
)

replace gopkg.aoctech.app/wallet/rpc-contract => ../rpc-contract

require (
	github.com/aws/aws-sdk-go-v2/credentials v1.20.7 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.20.1 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.4 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.4 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.5.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.19 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.14.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.10.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.38.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.43.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.51.2 // indirect
	github.com/aws/smithy-go v1.28.4 // indirect
	github.com/valkey-io/valkey-go v1.0.78 // indirect
	golang.org/x/sys v0.48.0 // indirect
)
