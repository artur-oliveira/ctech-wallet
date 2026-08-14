module gopkg.aoctech.app/wallet/pix-gateway

go 1.26.5

require (
	github.com/aws/aws-lambda-go v1.54.0
	github.com/aws/aws-sdk-go-v2 v1.43.5
	github.com/aws/aws-sdk-go-v2/config v1.32.36
	github.com/aws/aws-sdk-go-v2/service/ssm v1.73.5
	github.com/caarlos0/env/v11 v11.4.1
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
	gopkg.aoctech.app/api-commons v1.5.0
	gopkg.aoctech.app/wallet/rpc-contract v0.0.0
)

replace gopkg.aoctech.app/wallet/rpc-contract => ../rpc-contract

require (
	github.com/aws/aws-sdk-go-v2/credentials v1.19.35 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.36 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.36 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.36 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.37 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.16 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.36 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.5.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.33.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.38.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.45.5 // indirect
	github.com/aws/smithy-go v1.27.7 // indirect
	github.com/valkey-io/valkey-go v1.0.76 // indirect
	golang.org/x/sys v0.47.0 // indirect
)
