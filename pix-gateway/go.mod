module gopkg.aoctech.app/wallet/pix-gateway

go 1.26.5

require (
	github.com/aws/aws-lambda-go v1.54.0
	github.com/aws/aws-sdk-go-v2 v1.43.3
	github.com/aws/aws-sdk-go-v2/config v1.32.33
	github.com/aws/aws-sdk-go-v2/service/ssm v1.73.2
	github.com/caarlos0/env/v11 v11.4.1
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
	gopkg.aoctech.app/api-commons v1.4.0
	gopkg.aoctech.app/wallet/rpc-contract v0.0.0
)

replace gopkg.aoctech.app/wallet/rpc-contract => ../rpc-contract

require (
	github.com/aws/aws-sdk-go-v2/credentials v1.19.32 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.33 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.33 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.33 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.34 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.14 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.33 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.5.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.33.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.38.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.45.2 // indirect
	github.com/aws/smithy-go v1.27.6 // indirect
	github.com/valkey-io/valkey-go v1.0.76 // indirect
	golang.org/x/sys v0.47.0 // indirect
)
