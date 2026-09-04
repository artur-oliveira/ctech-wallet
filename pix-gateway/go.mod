module gopkg.aoctech.app/wallet/pix-gateway

go 1.26.6

require (
	github.com/aws/aws-lambda-go v1.54.0
	github.com/aws/aws-sdk-go-v2 v1.45.1
	github.com/aws/aws-sdk-go-v2/config v1.32.38
	github.com/aws/aws-sdk-go-v2/service/ssm v1.73.7
	github.com/caarlos0/env/v11 v11.4.1
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
	gopkg.aoctech.app/api-commons v1.8.0
	gopkg.aoctech.app/wallet/rpc-contract v0.0.0
)

replace gopkg.aoctech.app/wallet/rpc-contract => ../rpc-contract

require (
	github.com/aws/aws-sdk-go-v2/credentials v1.19.37 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.38 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.1 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.1 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.5.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.17 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.38 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.5.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.33.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.38.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.45.7 // indirect
	github.com/aws/smithy-go v1.28.1 // indirect
	github.com/valkey-io/valkey-go v1.0.77 // indirect
	golang.org/x/sys v0.47.0 // indirect
)
