import { readFile, writeFile } from 'node:fs/promises';
import { parse } from 'yaml';

const inventory = parse(await readFile('../../api/external/push-protocol-inventory.yaml', 'utf8'));
const socket = inventory?.mcs_socket;
if (!socket || !/^[a-z0-9.-]+$/.test(socket.host) || !Number.isInteger(socket.port) ||
    socket.port < 1 || socket.port > 65535 || socket.network !== 'tcp' || socket.tls !== true ||
    !Number.isInteger(socket.version) || socket.version < 0 || socket.version > 255 ||
    socket.callsite !== 'third_party/go-push-receiver/fcm.go:tryToConnect') {
  throw new Error('invalid MCS socket inventory');
}

const tags = Object.entries(socket.tags ?? {});
if (tags.length !== 18 || new Set(tags.map(([, value]) => value)).size !== tags.length ||
    tags.some(([name, value]) => !/^[A-Z][A-Za-z0-9]+$/.test(name) ||
      !Number.isInteger(value) || value < 0 || value > 255)) {
  throw new Error('invalid MCS tag inventory');
}

const goString = (value) => JSON.stringify(String(value));
const source = [
  '// Code generated from api/external/push-protocol-inventory.yaml. DO NOT EDIT.',
  '',
  'package protocol',
  '',
  'const (',
  `  MCSHost = ${goString(socket.host)}`,
  `  MCSPort = ${goString(socket.port)}`,
  `  MCSAddress = ${goString(`${socket.host}:${socket.port}`)}`,
  `  MCSNetwork = ${goString(socket.network)}`,
  `  MCSTLS = ${socket.tls}`,
  `  MCSDomain = ${goString(socket.domain)}`,
  `  MCSVersion = ${socket.version}`,
  `  MCSVersionPacketBytes = ${socket.version_bytes}`,
  `  MCSTagPacketBytes = ${socket.tag_bytes}`,
  `  MCSLengthEncoding = ${goString(socket.length_encoding)}`,
  ...tags.map(([name, value]) => `  MCS${name}Tag = ${value}`),
  ')',
  '',
].join('\n');
await writeFile('../../internal/protocol/mcs.gen.go', source);
