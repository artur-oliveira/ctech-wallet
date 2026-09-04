module gopkg.aoctech.app/wallet/api

go 1.26

require (
	github.com/aws/aws-lambda-go v1.54.0
	github.com/aws/aws-sdk-go-v2 v1.45.1
	github.com/aws/aws-sdk-go-v2/config v1.32.38
	github.com/aws/aws-sdk-go-v2/credentials v1.19.37
	github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue v1.20.62
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.66.0
	github.com/aws/aws-sdk-go-v2/service/lambda v1.101.3
	github.com/aws/aws-sdk-go-v2/service/ssm v1.73.5
	github.com/caarlos0/env/v11 v11.4.1
	github.com/fasthttp/websocket v1.5.12
	github.com/go-playground/validator/v10 v10.30.3
	github.com/gofiber/fiber/v3 v3.5.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/google/uuid v1.6.0
	github.com/oklog/ulid/v2 v2.1.2
	github.com/valyala/fasthttp v1.73.0
	go.uber.org/fx v1.24.0
	gopkg.aoctech.app/api-commons v1.9.1
	gopkg.aoctech.app/wallet/rpc-contract v0.0.0
)

replace gopkg.aoctech.app/wallet/rpc-contract => ../rpc-contract

require (
	github.com/andybalholm/brotli v1.2.2 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.17 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.38 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.1 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.1 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.5.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/dynamodbstreams v1.36.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.19 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.13.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.38 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.5.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.33.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.38.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.45.7 // indirect
	github.com/aws/smithy-go v1.28.1 // indirect
	github.com/gabriel-vasile/mimetype v1.4.15 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/gofiber/schema v1.8.4 // indirect
	github.com/gofiber/utils/v2 v2.4.1 // indirect
	github.com/klauspost/compress v1.19.2 // indirect
	github.com/leodido/go-urn v1.5.0 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/savsgio/gotils v0.0.0-20250924091648-bce9a52d7761 // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/valkey-io/valkey-go v1.0.77 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	go.uber.org/dig v1.19.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)
