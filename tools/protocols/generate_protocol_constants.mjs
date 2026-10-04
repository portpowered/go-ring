import { readFile, writeFile } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse } from 'yaml';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const check = process.argv.includes('--check');

const [openapi, asyncapi, fcm, clientModels] = await Promise.all([
  readSchema('api/openapi.yaml'),
  readSchema('api/asyncapi.yaml'),
  readSchema('api/external/fcm.openapi.yaml'),
  readSchema('api/client-models.openapi.yaml'),
]);

const endpoints = generateEndpoints();
const signaling = generateSignaling();
const fcmConstants = generateFCM();
const publicModelConstants = generatePublicModels();

await writeOrCheck('internal/protocol/endpoints.go', endpoints);
await writeOrCheck('internal/protocol/signaling.go', signaling);
await writeOrCheck('internal/protocol/fcm.go', fcmConstants);
await writeOrCheck('internal/protocol/public_models.go', publicModelConstants);

async function readSchema(path) {
  return parse(await readFile(resolve(root, path), 'utf8'));
}

function generateEndpoints() {
  const operationIDs = {
    OAuthTokenPath: 'exchangeOrRefreshOAuthToken',
    OAuthAuthorizePath: 'beginOrContinueOAuthAuthorization',
    OAuthSigninPath: 'submitOAuthCredentials',
    TicketPath: 'requestLegacySignalingTicket',
    DevicesV3Path: 'listDevices',
    DeviceDetailV3Path: 'getDevice',
    LocationListV3Path: 'listLocations',
    LocationDetailV4Path: 'getLocation',
    LocationGroupsPath: 'listLocationGroups',
    LocationDevicesPath: 'listLocationDevices',
    DeviceTimelinePath: 'getDeviceTimeline',
    HistoryDevicesPath: 'getHistoryDevices',
    CapturedTicketsPath: 'getCapturedLocationTickets',
    DeviceCommandPath: 'sendDeviceCommand',
    IntercomUnlockPath: 'unlockIntercom',
    PushDeviceRegistrationPath: 'registerPushDevice',
    DeviceDingSubscribePath: 'subscribeDeviceDing',
    DeviceMotionSubscribePath: 'subscribeDeviceMotion',
    PersistentLiveViewPath: 'setLiveViewEnabled',
    RecordingFavoritePath: 'favoriteRecording',
    RecordingDeletePath: 'deleteRecording',
    SessionPath: 'registerClientSession',
    DingsActivePath: 'getActiveDings',
    DoorbotHistoryPath: 'getLegacyDeviceHistory',
    RecordingPath: 'streamRecording',
    RecordingSharePath: 'getLegacyRecordingShareURL',
    DeviceSettingsPath: 'getDeviceSettings',
    DoorbotSirenOnPath: 'turnSirenOn',
    DoorbotSirenOffPath: 'turnSirenOff',
    LegacyChimePath: 'setChimeVolume',
    LegacyDoorbotPath: 'updateLegacyDoorbotControls',
    LegacyChimeSoundPath: 'testChimeSound',
    DoorbotLightOnPath: 'turnFloodlightOn',
    DoorbotLightOffPath: 'turnFloodlightOff',
    LegacySnapshotTimestampPath: 'refreshLegacySnapshotTimestamp',
    LegacySnapshotImagePath: 'getLegacySnapshotImage',
  };

  const constants = [
    ['APIBaseURL', requireString(openapi.servers?.[0]?.url, 'OpenAPI default server URL')],
    ['OAuthBaseURL', operationServer('beginOrContinueOAuthAuthorization')],
    ['USSolutionsBaseURL', operationServer('requestLegacySignalingTicket')],
  ];

  for (const [name, operationID] of Object.entries(operationIDs)) {
    constants.push([name, operationPath(operationID)]);
  }

  constants.push([
    'OAuthCallbackURL',
    parameterSchema('beginOrContinueOAuthAuthorization', 'redirect_uri').default,
  ]);
  constants.push(['SignalingURL', asyncChannelURL(asyncChannelNameForMessage('InboundDiscriminator'))]);
  constants.push(['ExperimentalEventWebSocketURL', asyncChannelURL(asyncChannelNameForMessage('AccountEvent'))]);

  return goFile('protocol', constants, {
    OAuthTokenPath: '// #nosec G101 -- endpoint path, not credential material.',
  });
}

function generateSignaling() {
  const channels = asyncapi.channels;
  const signalingName = asyncChannelNameForMessage('InboundDiscriminator');
  const eventsName = asyncChannelNameForMessage('AccountEvent');
  const signaling = requireObject(channels?.[signalingName], `AsyncAPI ${signalingName} channel`);
  const events = requireObject(channels?.[eventsName], `AsyncAPI ${eventsName} channel`);
  const signalingServer = asyncServer(signaling);
  const eventServer = asyncServer(events);
  const signalingURL = new URL(`${signalingServer.protocol}://${signalingServer.host}`);
  const eventURL = new URL(`${eventServer.protocol}://${eventServer.host}`);
  const queryProperties = requireObject(signaling.bindings?.ws?.query?.properties, 'signaling query properties');

  const constants = [
    ['SignalingChannel', signalingName],
    ['AccountEventsChannel', eventsName],
    ['signalingProtocol', signalingServer.protocol],
    ['signalingHost', signalingURL.hostname],
    ['signalingPort', effectivePort(signalingURL)],
    ['signalingPath', requireString(signaling.address, 'serverEnvelope address')],
    ['accountEventsProtocol', eventServer.protocol],
    ['accountEventsHost', eventURL.hostname],
    ['accountEventsPort', effectivePort(eventURL)],
    ['accountEventsPath', requireString(events.address, 'accountEvent address')],
  ];

  const queryKeys = [];
  for (const [key, schema] of Object.entries(queryProperties)) {
    const suffix = pascalCase(key);
    const constantName = `signalingQuery${suffix}Key`;
    queryKeys.push(constantName);
    constants.push([constantName, key]);
    if (schema.const !== undefined) {
      constants.push([`signaling${suffix}`, String(schema.const)]);
    } else if (schema.pattern !== undefined) {
      constants.push([`signaling${suffix}Prefix`, patternLiteralPrefix(schema.pattern)]);
    }
  }

  const methods = new Set([
    ...enumValues(asyncProperty('ClientEnvelope', 'method')),
    ...enumValues(asyncProperty('ServerEnvelope', 'method')),
  ]);
  for (const [schemaName, schema] of Object.entries(asyncapi.components?.schemas ?? {})) {
    const method = schema?.properties?.method;
    if (schemaName.endsWith('Frame') && method?.const !== undefined) methods.add(String(method.const));
  }

  const methodConstants = [...methods]
    .filter((value) => !value.startsWith('PTZ.'))
    .sort()
    .map((value) => [`Method${pascalCase(value, true)}`, value]);
  const rpcMethods = enumValues(asyncProperty('PTZRPC', 'method'))
    .map((value) => [`RPC${value.split('.').slice(1).map(pascalCase).join('')}`, value]);
  constants.push(...methodConstants, ...rpcMethods);

  constants.push(
    ['JSONRPCVersion', schemaConst(asyncProperty('PTZRPC', 'jsonrpc'), 'PTZRPC.jsonrpc')],
    ['PTZVersion', schemaConst(asyncProperty('PTZRPC', 'params', 'version'), 'PTZRPC.params.version')],
    ['PTZMaxSpeed', schemaNumber(asyncProperty('PTZRPC', 'params', 'speed')?.maximum, 'PTZRPC.params.speed.maximum')],
  );

  const directionByAxis = movementDirections(requireObject(asyncapi.components.schemas.PTZRPC, 'PTZRPC schema'));
  for (const axis of ['Pan', 'Tilt']) {
    for (const direction of directionByAxis[axis]) {
      constants.push([`${axis}${pascalCase(direction)}`, direction]);
    }
  }

  constants.push(
    ['SDPTypeOffer', schemaConst(asyncProperty('LiveViewBody', 'type'), 'LiveViewBody.type')],
    ['SDPTypeAnswer', schemaConst(asyncProperty('LiveAnswerBody', 'type'), 'LiveAnswerBody.type')],
    ['PlaybackOfferTypeCloud', schemaConst(asyncProperty('PlaybackOfferBody', 'type'), 'PlaybackOfferBody.type')],
    ['PlaybackCloseReasonClientClosed', schemaConst(asyncProperty('PlaybackCloseReason', 'text'), 'PlaybackCloseReason.text')],
  );

  const playbackEntryPoints = asyncProperty('PlaybackOfferBody', 'entry_point')?.['x-extensible-enum'];
  if (!Array.isArray(playbackEntryPoints) || playbackEntryPoints.length !== 1) {
    throw new Error('PlaybackOfferBody.entry_point must list its observed default value');
  }
  constants.push(['PlaybackEntryPointTimeline', String(playbackEntryPoints[0])]);

  const playbackAnswerType = schemaConst(asyncProperty('PlaybackAnswerBody', 'type'), 'PlaybackAnswerBody.type');
  if (playbackAnswerType !== schemaConst(asyncProperty('LiveAnswerBody', 'type'), 'LiveAnswerBody.type')) {
    throw new Error('PlaybackAnswerBody.type does not match LiveAnswerBody.type');
  }

  const statusValues = asyncProperty('PushSubscriptionAckBody', 'status')?.['x-extensible-enum'];
  if (!Array.isArray(statusValues) || statusValues.length !== 1) {
    throw new Error('PushSubscriptionAckBody.status must list its observed status value');
  }
  constants.push(['SubscriptionStatusOK', String(statusValues[0])]);

  constants.push(
    ['SDPSendRecv', enumValueAt(asyncProperty('LiveViewBody', 'sdp')['x-sdp-media-direction'], 0)],
    ['SDPSendOnly', enumValueAt(asyncProperty('LiveViewBody', 'sdp')['x-sdp-media-direction'], 1)],
    ['SDPRecvOnly', enumValueAt(asyncProperty('LiveViewBody', 'sdp')['x-sdp-media-direction'], 2)],
    ['SDPInactive', enumValueAt(asyncProperty('LiveViewBody', 'sdp')['x-sdp-media-direction'], 3)],
  );

  const fieldMappings = [
    ['FieldMethod', ['SignalingInboundDiscriminator', 'method']],
    ['FieldDialogID', ['SignalingInboundDiscriminator', 'dialog_id']],
    ['FieldRIID', ['SignalingInboundDiscriminator', 'riid']],
    ['FieldBody', ['SignalingInboundDiscriminator', 'body']],
    ['FieldDeviceID', ['SessionBody', 'doorbot_id']],
    ['FieldSessionID', ['SessionBody', 'session_id']],
    ['FieldIce', ['LiveICEBody', 'ice']],
    ['FieldMID', ['LiveICEBody', 'mid']],
    ['FieldMLineIndex', ['LiveICEBody', 'mlineindex']],
    ['FieldIsOK', ['SessionNotificationBody', 'is_ok']],
    ['FieldText', ['SessionNotificationBody', 'text']],
    ['FieldNotificationScope', ['PushEventBody', 'notification_scope']],
    ['FieldNotificationType', ['PushEventBody', 'notification_type']],
    ['FieldPayload', ['PushEventBody', 'payload']],
    ['FieldSubscriptionID', ['PushSubscriptionAckBody', 'subscription_id']],
    ['FieldStatus', ['PushSubscriptionAckBody', 'status']],
    ['FieldSessionInfo', ['LiveAnswerBody', 'session_info']],
    ['FieldSDP', ['LiveViewBody', 'sdp']],
    ['FieldStreamOptions', ['LiveViewBody', 'stream_options']],
    ['FieldType', ['LiveViewBody', 'type']],
    ['FieldEntryPoint', ['PlaybackOfferBody', 'entry_point']],
    ['FieldRequestedNotifications', ['PushSubscribeBody', 'requested_notifications']],
    ['FieldJSONRPC', ['PTZWireCommand', 'jsonrpc']],
    ['FieldRPCID', ['PTZWireCommand', 'id']],
    ['FieldParams', ['PTZWireCommand', 'params']],
    ['FieldResult', ['ServerRPCCommand', 'result']],
    ['FieldError', ['ServerRPCCommand', 'error']],
    ['FieldCode', ['ServerCloseReason', 'code']],
    ['FieldReason', ['PlaybackCloseBody', 'reason']],
    ['FieldPingInterval', ['LiveAnswerInfo', 'ping_interval']],
    ['FieldEnabled', ['SessionMicrophoneBody', 'enabled']],
    ['FieldAudioEnabled', ['SessionStreamAudioOptionsBody', 'audio_enabled']],
    ['FieldVideoEnabled', ['SessionStreamVideoOptionsBody', 'video_enabled']],
    ['FieldDirection', ['PTZRPC', 'params', 'direction']],
    ['FieldSpeed', ['PTZRPC', 'params', 'speed']],
    ['FieldSessionIDRPC', ['PTZRPC', 'params', 'sessionId']],
    ['FieldTimestamp', ['PTZRPC', 'params', 'timestamp']],
    ['FieldVersion', ['PTZRPC', 'params', 'version']],
    ['FieldCommand', ['ServerRPCBody', 'command']],
  ];
  for (const [constantName, [schemaName, ...keys]] of fieldMappings) {
    constants.push([constantName, requirePropertyKey(schemaName, keys)]);
  }

  const queryHelper = [
    '',
    'func signalingQueryKeys() []string {',
    '\treturn []string{' + queryKeys.join(', ') + '}',
    '}',
  ].join('\n');
  return goFile('protocol', constants, {}, queryHelper);
}

function generateFCM() {
  const operationNames = {
    FCMCheckinHost: 'checkInFCMClient',
    FCMCheckinPath: 'checkInFCMClient',
    FCMRegisterHost: 'registerFCMClient',
    FCMRegisterPath: 'registerFCMClient',
    FCMInstallationsHost: 'createFCMInstallation',
    FCMInstallationsPath: 'createFCMInstallation',
    FCMRegistrationsHost: 'registerFCMInstallation',
    FCMRegistrationsPath: 'registerFCMInstallation',
  };
  const constants = [];
  for (const [name, operationID] of Object.entries(operationNames)) {
    const operation = fcmOperation(operationID);
    constants.push([name, name.endsWith('Host') ? new URL(operationServer(operationID, fcm)).hostname : operation.path]);
  }

  const installationRequest = fcm.components.schemas.InstallationRequest;
  const legacyRequest = fcm.components.schemas.LegacyRegistrationRequest;
  const installationResponse = fcm.components.schemas.InstallationResponse;
  const installationAuthToken = fcm.components.schemas.InstallationAuthToken;
  const registrationResponse = fcm.components.schemas.FCMRegistrationResponse;
  const registrationWeb = fcm.components.schemas.FCMWebRegistration;
  const receiverAdapter = requireObject(fcm['x-pinned-receiver-adapter'], 'pinned receiver adapter extension');

  constants.push(
    ['FCMProjectID', projectIDFromPaths()],
    ['FCMApplicationID', constProperty(installationRequest, 'appId', 'InstallationRequest.appId')],
    ['FCMAPIKey', schemaConst(fcmComponentParameter('createFCMInstallation', 'GoogleAPIKey').schema, 'GoogleAPIKey')],
    ['FCMInstallationsAuthHeader', fcmAdapterAuthHeaderName('registerFCMInstallation')],
    ['FCMInstallationsAPIKeyHeader', requireString(fcmComponentParameter('createFCMInstallation', 'GoogleAPIKey').name, 'GoogleAPIKey name')],
    ['FCMContentTypeHeader', requestContentTypeHeaderName('registerFCMClient')],
    ['FCMAcceptHeader', requireString(fcmComponentParameter('createFCMInstallation', 'JSONAccept').name, 'JSONAccept name')],
    ['FCMContentTypeProtobuf', requestMediaTypeForSchema('checkInFCMClient', 'AndroidCheckinRequestWire')],
    ['FCMContentTypeForm', requestMediaTypeForSchema('registerFCMClient', 'LegacyRegistrationRequest')],
    ['FCMContentTypeJSON', schemaConst(fcmComponentParameter('createFCMInstallation', 'JSONContentType').schema, 'JSONContentType')],
    ['FCMInstallationsAuthPrefix', schemaConst(receiverAdapter.properties?.installations_auth_prefix, 'pinned receiver installations_auth_prefix')],
    ['FCMInstallationsAuthVersion', constProperty(installationRequest, 'authVersion', 'InstallationRequest.authVersion')],
    ['FCMInstallationsSDKVersion', constProperty(installationRequest, 'sdkVersion', 'InstallationRequest.sdkVersion')],
    ['FCMDefaultVAPIDKey', constProperty(legacyRequest, 'sender', 'LegacyRegistrationRequest.sender')],
    ['FCMRegistrationEndpointPrefix', patternLiteralPrefix(propertySchema(registrationWeb, 'endpoint', 'FCMWebRegistration.endpoint').pattern)],
    ['FCMAndroidAppIdentifier', constProperty(legacyRequest, 'app', 'LegacyRegistrationRequest.app')],
  );

  const fieldMappings = [
    ['FCMInstallationsAppIDKey', installationRequest, 'appId', 'InstallationRequest.appId'],
    ['FCMInstallationsAuthVersionKey', installationRequest, 'authVersion', 'InstallationRequest.authVersion'],
    ['FCMInstallationsSDKVersionKey', installationRequest, 'sdkVersion', 'InstallationRequest.sdkVersion'],
    ['FCMRegisterFormAppKey', legacyRequest, 'app', 'LegacyRegistrationRequest.app'],
    ['FCMRegisterFormSubtypeKey', legacyRequest, 'X-subtype', 'LegacyRegistrationRequest.X-subtype'],
    ['FCMRegisterFormDeviceKey', legacyRequest, 'device', 'LegacyRegistrationRequest.device'],
    ['FCMRegisterFormSenderKey', legacyRequest, 'sender', 'LegacyRegistrationRequest.sender'],
    ['FCMInstallationsNameKey', installationResponse, 'name', 'InstallationResponse.name'],
    ['FCMInstallationsFIDKey', installationResponse, 'fid', 'InstallationResponse.fid'],
    ['FCMInstallationsRefreshTokenKey', installationResponse, 'refreshToken', 'InstallationResponse.refreshToken'],
    ['FCMInstallationsAuthTokenKey', installationResponse, 'authToken', 'InstallationResponse.authToken'],
    ['FCMInstallationsTokenKey', installationAuthToken, 'token', 'InstallationAuthToken.token'],
    ['FCMInstallationsExpiresInKey', installationAuthToken, 'expiresIn', 'InstallationAuthToken.expiresIn'],
    ['FCMRegistrationWebKey', fcm.components.schemas.FCMRegistrationRequest, 'web', 'FCMRegistrationRequest.web'],
    ['FCMRegistrationEndpointKey', registrationWeb, 'endpoint', 'FCMWebRegistration.endpoint'],
    ['FCMRegistrationP256DHKey', registrationWeb, 'p256dh', 'FCMWebRegistration.p256dh'],
    ['FCMRegistrationAuthKey', registrationWeb, 'auth', 'FCMWebRegistration.auth'],
    ['FCMRegistrationVAPIDKey', receiverAdapter.properties?.registration_web_vapid_key, null, 'pinned receiver registration_web_vapid_key'],
    ['FCMRegistrationTokenKey', registrationResponse, 'token', 'FCMRegistrationResponse.token'],
    ['FCMRegistrationPushSetKey', registrationResponse, 'pushSet', 'FCMRegistrationResponse.pushSet'],
    ['FCMPushAndroidConfigKey', fcm.components.schemas.RingPushNotificationEnvelope, 'android_config', 'RingPushNotificationEnvelope.android_config'],
    ['FCMPushDataKey', fcm.components.schemas.RingPushNotificationEnvelope, 'data', 'RingPushNotificationEnvelope.data'],
    ['FCMPushDoorbotIDKey', fcm.components.schemas.RingPushNotificationEnvelope, 'doorbot_id', 'RingPushNotificationEnvelope.doorbot_id'],
    ['FCMPushCategoryKey', fcm.components.schemas.RingPushNotificationConfig, 'category', 'RingPushNotificationConfig.category'],
    ['FCMPushPayloadDeviceKey', fcm.components.schemas.RingPushNotificationPayload, 'device', 'RingPushNotificationPayload.device'],
    ['FCMPushPayloadGCMDataKey', fcm.components.schemas.RingPushNotificationPayload, 'gcmData', 'RingPushNotificationPayload.gcmData'],
    ['FCMPushDeviceIDKey', fcm.components.schemas.RingPushNotificationDevice, 'id', 'RingPushNotificationDevice.id'],
    ['FCMPushGCMActionKey', fcm.components.schemas.RingPushNotificationGCMData, 'action', 'RingPushNotificationGCMData.action'],
  ];

  for (const [name, schema, key, label] of fieldMappings) {
    const value = key === null ? schemaConst(schema, label) : propertyKey(schema, key, label);
    constants.push([name, value]);
  }

  return goFile('protocol', constants, {
    FCMAPIKey: '// #nosec G101 -- public Firebase client key in the pinned receiver protocol.',
    FCMDefaultVAPIDKey: '// #nosec G101 -- public VAPID application key from the pinned receiver protocol.',
  });
}

function generatePublicModels() {
  const mappings = [
    ['DeviceKindStickUpMiniPTZ', 'DeviceKind', 'stickup_cam_mini_ptz_v1'],
    ['ConnectionOnline', 'ConnectionState', 'online'],
    ['ConnectionOffline', 'ConnectionState', 'offline'],
    ['PowerModeWired', 'PowerMode', 'wired'],
    ['LocationResourceLocations', 'LocationResourceType', 'locations'],
    ['TimelineEventOnDemand', 'TimelineEventType', 'on_demand'],
    ['TimelineEventDing', 'TimelineEventType', 'ding'],
    ['TimelineEventMotion', 'TimelineEventType', 'motion'],
    ['RecordingStatusReady', 'RecordingStatus', 'ready'],
    ['TimelineStateCompleted', 'TimelineState', 'completed'],
    ['HistoryFeedEvent', 'HistoryFeedType', 'EVENT'],
  ];
  const constants = mappings.map(([constantName, schemaName, expectedValue]) => {
    const schema = requireObject(clientModels.components?.schemas?.[schemaName], `${schemaName} projection schema`);
    const values = schema['x-extensible-enum'];
    if (!Array.isArray(values) || !values.includes(expectedValue)) {
      throw new Error(`${schemaName} must list ${expectedValue} as a known open value`);
    }
    return [constantName, expectedValue];
  });
  return goFile('protocol', constants);
}

function operationPath(operationID, document = openapi) {
  const match = findOperation(operationID, document);
  return match.path;
}

function operationServer(operationID, document = openapi) {
  const match = findOperation(operationID, document);
  const server = match.operation.servers?.[0]?.url ?? document.servers?.[0]?.url;
  return requireString(server, `server for ${operationID}`);
}

function findOperation(operationID, document = openapi) {
  const matches = [];
  for (const [path, pathItem] of Object.entries(document.paths ?? {})) {
    for (const [method, operation] of Object.entries(pathItem ?? {})) {
      if (operation?.operationId === operationID) matches.push({ path, method, operation });
    }
  }
  if (matches.length !== 1) throw new Error(`${operationID}: expected one operation, found ${matches.length}`);
  return matches[0];
}

function parameterSchema(operationID, name, document = openapi) {
  const { operation } = findOperation(operationID, document);
  for (const reference of operation.parameters ?? []) {
    const parameter = resolveRef(document, reference);
    if (parameter.name === name) return requireObject(parameter.schema, `${operationID}.${name} schema`);
  }
  throw new Error(`${operationID}: parameter ${name} is missing`);
}

function asyncChannelURL(name) {
  const channel = requireObject(asyncapi.channels?.[name], `AsyncAPI channel ${name}`);
  const server = asyncServer(channel);
  const properties = channel.bindings?.ws?.query?.properties ?? {};
  const query = Object.entries(properties).map(([key, property]) => {
    const value = property.const !== undefined
      ? String(property.const)
      : property.pattern !== undefined
        ? `${patternLiteralPrefix(property.pattern)}{${key}}`
        : `{${key}}`;
    return `${key}=${value}`;
  });
  return `${server.protocol}://${server.host}${requireString(channel.address, `${name} address`)}${query.length ? `?${query.join('&')}` : ''}`;
}

function asyncChannelNameForMessage(messageName) {
  const reference = `#/components/messages/${messageName}`;
  const matches = Object.entries(asyncapi.channels ?? {}).filter(([, channel]) =>
    Object.values(channel.messages ?? {}).some((message) => message?.$ref === reference));
  if (matches.length !== 1) throw new Error(`expected one AsyncAPI channel carrying ${messageName}, found ${matches.length}`);
  return matches[0][0];
}

function asyncServer(channel) {
  const reference = channel.servers?.[0]?.$ref;
  if (typeof reference !== 'string') throw new Error('AsyncAPI channel must reference a server');
  const name = reference.replace(/^#\/servers\//, '');
  if (name === reference) throw new Error(`unsupported AsyncAPI server reference ${reference}`);
  const server = requireObject(asyncapi.servers?.[name], `AsyncAPI server ${name}`);
  return {
    protocol: requireString(server.protocol, `AsyncAPI server ${name}.protocol`),
    host: requireString(server.host, `AsyncAPI server ${name}.host`),
  };
}

function effectivePort(url) {
  if (url.port) return url.port;
  if (url.protocol === 'wss:') return '443';
  if (url.protocol === 'ws:') return '80';
  throw new Error(`WebSocket scheme ${url.protocol} has no known default port`);
}

function asyncProperty(schemaName, ...path) {
  let current = requireObject(asyncapi.components?.schemas?.[schemaName], `AsyncAPI schema ${schemaName}`);
  for (const key of path) current = requireObject(current.properties?.[key], `${schemaName}.${path.join('.')}`);
  return current;
}

function enumValues(schema) {
  const values = new Set();
  if (Array.isArray(schema?.enum)) for (const value of schema.enum) values.add(String(value));
  if (schema?.const !== undefined) values.add(String(schema.const));
  return [...values];
}

function movementDirections(schema) {
  const axes = { Pan: [], Tilt: [] };
  for (const rule of schema.allOf ?? []) {
    const methods = rule.if?.properties?.method?.enum ?? [];
    const directions = rule.then?.properties?.params?.properties?.direction?.enum ?? [];
    if (methods.length === 0 || directions.length === 0) continue;
    const axis = methods.some((value) => String(value).includes('.Pan.'))
      ? 'Pan'
      : methods.some((value) => String(value).includes('.Tilt.'))
        ? 'Tilt'
        : undefined;
    if (axis) axes[axis] = directions.map(String);
  }
  if (axes.Pan.length !== 2 || axes.Tilt.length !== 2) {
    throw new Error('PTZRPC must associate two direction values with each pan/tilt axis');
  }
  return axes;
}

function enumValueAt(schema, index) {
  const values = requireObject(schema, 'SDP media direction extension').enum;
  if (!Array.isArray(values) || values.length !== 4) throw new Error('SDP media direction extension must list the four standard values');
  return String(values[index]);
}

function requirePropertyKey(schemaName, path) {
  let schema = requireObject(asyncapi.components?.schemas?.[schemaName], `AsyncAPI schema ${schemaName}`);
  for (let index = 0; index < path.length; index += 1) {
    const key = path[index];
    if (index === path.length - 1) {
      if (!Object.hasOwn(schema.properties ?? {}, key)) throw new Error(`${schemaName}: property ${path.join('.')} is missing`);
      return key;
    }
    schema = requireObject(schema.properties?.[key], `${schemaName}.${path.slice(0, index + 1).join('.')}`);
  }
  throw new Error(`${schemaName}: empty property path`);
}

function fcmOperation(operationID) {
  return findOperation(operationID, fcm);
}

function fcmComponentParameter(operationID, componentName) {
  const { operation } = fcmOperation(operationID);
  const suffix = `/components/parameters/${componentName}`;
  for (const reference of operation.parameters ?? []) {
    if (String(reference?.$ref).endsWith(suffix)) return resolveRef(fcm, reference);
  }
  throw new Error(`${operationID}: parameter component ${componentName} is missing`);
}

function fcmAdapterAuthHeaderName(operationID) {
  const { operation } = fcmOperation(operationID);
  const matches = (operation.parameters ?? []).map((reference) => resolveRef(fcm, reference)).filter((parameter) =>
    parameter.in === 'header' && parameter.required && parameter.schema?.minLength === 1 && parameter.schema?.const === undefined);
  if (matches.length !== 1) throw new Error(`${operationID}: expected one opaque installation authorization header`);
  return requireString(matches[0].name, `${operationID} installation authorization header name`);
}

function requestMediaTypeForSchema(operationID, schemaName) {
  const { operation } = fcmOperation(operationID);
  const matches = Object.entries(operation.requestBody?.content ?? {}).filter(([, media]) =>
    media.schema?.$ref === `#/components/schemas/${schemaName}`);
  if (matches.length !== 1) throw new Error(`${operationID}: expected one request body using ${schemaName}`);
  return matches[0][0];
}

function requestContentTypeHeaderName(operationID) {
  const mediaType = requestMediaTypeForSchema(operationID, 'LegacyRegistrationRequest');
  const { operation } = fcmOperation(operationID);
  const matches = (operation.parameters ?? []).map((reference) => resolveRef(fcm, reference)).filter((parameter) =>
    parameter.in === 'header' && parameter.required && parameter.schema?.const === mediaType);
  if (matches.length !== 1) throw new Error(`${operationID}: request media type does not identify one required header`);
  return requireString(matches[0].name, `${operationID} request Content-Type header name`);
}

function fcmProjectID() {
  const values = ['createFCMInstallation', 'registerFCMInstallation'].map((operationID) => {
    const path = fcmOperation(operationID).path;
    const match = /^\/v1\/projects\/([^/]+)\//.exec(path);
    if (!match) throw new Error(`${operationID}: path does not identify a project ID`);
    return match[1];
  });
  if (values[0] !== values[1]) throw new Error('FCM installation paths disagree on project ID');
  return values[0];
}

function projectIDFromPaths() {
  return fcmProjectID();
}

function constProperty(schema, key, label) {
  return schemaConst(propertySchema(schema, key, label), label);
}

function propertySchema(schema, key, label) {
  return requireObject(schema?.properties?.[key], label);
}

function propertyKey(schema, key, label) {
  if (!Object.hasOwn(schema?.properties ?? {}, key)) throw new Error(`${label}: property key is missing`);
  return key;
}

function schemaConst(schema, label) {
  if (typeof schema?.const !== 'string' && typeof schema?.const !== 'number') {
    throw new Error(`${label} must have a string or numeric const value`);
  }
  return schema.const;
}

function schemaNumber(value, label) {
  if (typeof value !== 'number' || !Number.isFinite(value)) throw new Error(`${label} must be a finite number`);
  return value;
}

function patternLiteralPrefix(pattern) {
  if (typeof pattern !== 'string' || !pattern.startsWith('^')) throw new Error(`expected anchored pattern, got ${pattern}`);
  let prefix = '';
  for (let index = 1; index < pattern.length; index += 1) {
    const character = pattern[index];
    if (character === '\\') {
      index += 1;
      if (index >= pattern.length) throw new Error(`invalid regex escape in ${pattern}`);
      prefix += pattern[index];
      continue;
    }
    if ('[](){}.*+?|$'.includes(character)) break;
    prefix += character;
  }
  if (prefix.length === 0) throw new Error(`pattern ${pattern} has no literal prefix`);
  return prefix;
}

function pascalCase(value, acronymAware = false) {
  return String(value)
    .split(/[^A-Za-z0-9]+/)
    .filter(Boolean)
    .map((part) => {
      const lower = part.toLowerCase();
      if (acronymAware && ({ ice: 'ICE', sdp: 'SDP', rpc: 'RPC' })[lower]) return ({ ice: 'ICE', sdp: 'SDP', rpc: 'RPC' })[lower];
      return part.charAt(0).toUpperCase() + part.slice(1).toLowerCase();
    })
    .join('');
}

function resolveRef(document, value) {
  if (value?.$ref === undefined) return value;
  if (!String(value.$ref).startsWith('#/')) throw new Error(`only local schema references are supported: ${value.$ref}`);
  return String(value.$ref).slice(2).split('/').reduce((current, part) => {
    const key = part.replaceAll('~1', '/').replaceAll('~0', '~');
    return current?.[key];
  }, document);
}

function requireString(value, label) {
  if (typeof value !== 'string' || value.length === 0) throw new Error(`${label} must be a nonempty string`);
  return value;
}

function requireObject(value, label) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) throw new Error(`${label} must be an object`);
  return value;
}

function goFile(packageName, constants, comments = {}, suffix = '') {
  const declarations = constants.map(([name, value]) => {
    if (typeof value === 'number') return `${comments[name] ? `${comments[name]}\n` : ''}\t${name} = ${value}`;
    return `${comments[name] ? `${comments[name]}\n` : ''}\t${name} = ${JSON.stringify(String(value))}`;
  });
  const body = [
    '// Code generated by tools/protocols/generate_protocol_constants.mjs. DO NOT EDIT.',
    '',
    `package ${packageName}`,
    '',
    'const (',
    ...declarations,
    ')',
    suffix,
    '',
  ].filter((line, index, all) => !(line === '' && all[index - 1] === '')).join('\n');
  return execFileSync('gofmt', [], { input: body.replaceAll('\\/', '/'), encoding: 'utf8' });
}

async function writeOrCheck(relative, expected) {
  const path = resolve(root, relative);
  let actual;
  try {
    actual = await readFile(path, 'utf8');
  } catch (error) {
    if (error.code !== 'ENOENT' || check) throw error;
  }
  if (actual === expected) return;
  if (check) throw new Error(`${relative} is out of date; run make generate-api`);
  await writeFile(path, expected);
}
