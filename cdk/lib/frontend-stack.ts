import * as cdk from 'aws-cdk-lib';
import * as cloudfront from 'aws-cdk-lib/aws-cloudfront';
import * as s3 from 'aws-cdk-lib/aws-s3';
import {createNextjsStaticFrontend} from '@aoctech/cdk';
import {Construct} from 'constructs';
import {Environment} from './types';
import {
  API_PATH_PATTERNS,
  DEFAULT_PUBLIC_LOCALE,
  ENGLISH_PUBLIC_LOCALE,
  frontendBucketName,
  LOCALE_COOKIE_NAME,
  LOCALIZED_PUBLIC_ROUTES,
  routeStoreName,
  SERVICE,
} from './constants';

interface FrontendStackProps extends cdk.StackProps {
  environment: Environment;
  certificateArn: string;
  domainName?: string;
  apiDomainName: string;
  authDomainName: string;
  extraConnectSrc: string[];
}

export class FrontendStack extends cdk.Stack {
  public readonly bucket: s3.Bucket;
  public readonly distribution: cloudfront.Distribution;
  public readonly routeStore: cloudfront.KeyValueStore;

  constructor(scope: Construct, id: string, props: FrontendStackProps) {
    super(scope, id, props);

    const rewriteFunctionCode = `
import cf from 'cloudfront';

const kvs = cf.kvs();
const localizedRoutes = ${JSON.stringify(Object.fromEntries(LOCALIZED_PUBLIC_ROUTES.map((route) => [route, true])))};

function preferredLocale(request) {
  var localeCookie = request.cookies && request.cookies['${LOCALE_COOKIE_NAME}'];
  if (localeCookie && localeCookie.value === '${ENGLISH_PUBLIC_LOCALE}') return '${ENGLISH_PUBLIC_LOCALE}';
  if (localeCookie && localeCookie.value === '${DEFAULT_PUBLIC_LOCALE}') return '${DEFAULT_PUBLIC_LOCALE}';
  var acceptLanguage = request.headers['accept-language'];
  return acceptLanguage && acceptLanguage.value.toLowerCase().indexOf('en') === 0
    ? '${ENGLISH_PUBLIC_LOCALE}'
    : '${DEFAULT_PUBLIC_LOCALE}';
}

async function handler(event) {
  var uri = event.request.uri;
  if (localizedRoutes[uri]) {
    var locale = preferredLocale(event.request);
    var suffix = uri === '/' ? '' : uri;
    var target = '/' + locale + suffix;
    if (await kvs.exists(target)) {
      return {
        statusCode: 307,
        statusDescription: 'Temporary Redirect',
        headers: {
          location: {value: target},
          'cache-control': {value: 'no-store'},
          vary: {value: 'Accept-Language, Cookie'},
        },
      };
    }
    event.request.uri = '/index.html';
    return event.request;
  }
  if (/\\.[^/]+$/.test(uri)) return event.request;
  var route = uri.endsWith('/') ? uri.slice(0, -1) : uri;
  event.request.uri = (await kvs.exists(route)) ? route + '.html' : '/404.html';
  return event.request;
}`;
    const connectSrc = [
      `https://${props.apiDomainName}`,
      `https://${props.authDomainName}`,
      ...props.extraConnectSrc.map((host) => `https://${host}`),
      `wss://${props.apiDomainName}`,
    ];
    const {bucket, distribution, routeStore} = createNextjsStaticFrontend(this, {
      environment: props.environment,
      serviceName: SERVICE,
      bucketName: frontendBucketName(props.environment),
      routeStoreName: routeStoreName(props.environment),
      apiDomainName: props.apiDomainName,
      apiPathPatterns: API_PATH_PATTERNS,
      connectSrc,
      domainName: props.domainName,
      certificateArn: props.domainName ? props.certificateArn : undefined,
      distributionComment: `CTech Wallet Frontend - ${props.environment}`,
      rewriteFunctionCode,
      outputExportNamePrefix: id,
    });

    this.bucket = bucket;
    this.distribution = distribution;
    this.routeStore = routeStore;
  }
}
