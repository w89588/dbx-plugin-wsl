#!/usr/bin/env node
/**
 * 契约级 Mock Sidecar（仅测试用）
 *
 * 用途：在没有真实 backend（或真实 backend 尚不可运行）时验证 tools/sidecar-probe.mjs
 * 与 tools/ui-cdp-check.mjs 的 harness 逻辑本身。实现范围覆盖探针使用到的
 * rpc-contract.md v1 方法/事件，数据为契约示例形状的内存假数据。
 *
 * 启动: node tests/fixtures/mock-sidecar.mjs
 * 探针: node tools/sidecar-probe.mjs --sidecar=tests/fixtures/mock-sidecar.mjs
 *
 * 注意：它不是产品后端，不得用于验收真实功能。
 */

import { randomBytes } from 'node:crypto';
import process from 'node:process';

const PLUGIN_ID = 'com.dbx.wsl';
const PLUGIN_VERSION = '0.1.0';
const PROTOCOL_VERSION = 1;

const state = {
  distros: [
    {
      name: 'Debian',
      guid: '{bcf36fb8-1959-4aff-9873-739ed04dd3e4}',
      version: 2,
      state: 'Stopped',
      running: false,
      isDefault: true,
      basePath: 'C:\\Users\\qa\\AppData\\Local\\Packages\\Debian\\LocalState',
      vhdPath: 'C:\\Users\\qa\\AppData\\Local\\Packages\\Debian\\LocalState\\ext4.vhdx',
      vhdBytes: 123456789,
      sparse: null,
      defaultUid: 1000,
    },
  ],
  images: process.env.MOCK_EMPTY_IMAGES === '1' ? [] : [
    {
      id: 'bf8527eb54c3',
      repository: 'alpine',
      tag: '3.20',
      digest: '<none>',
      createdAt: '2026-04-17 07:53:26 +0800 GMT+8',
      createdSince: '6 months ago',
      size: '7.81MB',
      containers: '0',
      uniqueSize: 'N/A',
    },
  ],
  containers: new Map(),
  volumes: new Map(),
  networks: new Map([
    ['bridge', { id: '16d2bf193261', name: 'bridge', driver: 'bridge', scope: 'local', ipv4: true, ipv6: false, internal: false, createdAt: '2026-10-20 14:48:36 +0000 UTC', labels: '' }],
    ['host', { id: 'e9525952b9d5', name: 'host', driver: 'host', scope: 'local', ipv4: true, ipv6: false, internal: false, createdAt: '2026-10-20 14:42:28 +0000 UTC', labels: '' }],
    ['none', { id: 'c873ff26ddac', name: 'none', driver: 'null', scope: 'local', ipv4: true, ipv6: false, internal: false, createdAt: '2026-10-20 14:42:28 +0000 UTC', labels: '' }],
  ]),
  jobs: new Map(),
  logins: new Map(),
  nextJobId: 1,
};

let nextContainerId = 1;
function containerId() {
  nextContainerId += 1;
  return (0x7075_0000_0000_0000n + BigInt(nextContainerId)).toString(16).padStart(16, '0').split('').map((c) => c).join('') + randomBytes(24).toString('hex');
}

function rpcResult(id, result) {
  write({ jsonrpc: '2.0', id, result });
}

function rpcError(id, code, message, data) {
  write({ jsonrpc: '2.0', id, error: { code, message, ...(data ? { data } : {}) } });
}

function write(message) {
  process.stdout.write(`${JSON.stringify(message)}\n`);
}

function emit(method, params) {
  write({ jsonrpc: '2.0', method, params });
}

function pluginError(code, message, hint, detail) {
  return { code, message, hint, detail };
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function createJob(method, params) {
  const jobId = `job-${state.nextJobId++}`;
  const job = {
    jobId,
    method,
    status: 'running',
    startedAt: Date.now(),
    finishedAt: null,
    exitCode: null,
    error: null,
    lines: [],
    timers: [],
    cancelled: false,
    result: null,
  };
  state.jobs.set(jobId, job);
  return job;
}

function jobOutput(job, stream, text) {
  if (job.status !== 'running') return;
  const line = { stream, text, ts: Date.now() };
  job.lines.push(line);
  emit('job/output', { jobId: job.jobId, ...line });
}

function jobEnd(job, { ok, exitCode, error = null, result = null } = {}) {
  if (job.status !== 'running') return;
  for (const timer of job.timers) clearTimeout(timer);
  job.timers = [];
  job.status = ok ? 'completed' : job.cancelled ? 'cancelled' : 'failed';
  job.exitCode = exitCode;
  job.error = error;
  job.result = result;
  job.finishedAt = Date.now();
  emit('job/end', {
    jobId: job.jobId,
    ok,
    exitCode,
    error,
    result,
    durationMs: job.finishedAt - job.startedAt,
  });
}

/** 向当前所有运行中的 wslc 事件订阅 job 推送 wslc/event。 */
function emitWSLCContainerEvent(action, name) {
  for (const job of state.jobs.values()) {
    if (job.method !== 'wslc/events/start' || job.status !== 'running') continue;
    emit('wslc/event', {
      jobId: job.jobId,
      event: {
        Action: action,
        Actor: { ID: containerId(), Attributes: { name } },
        Type: 'container',
        from: 'alpine:3.20',
        id: randomBytes(32).toString('hex'),
        scope: 'local',
        status: action,
        time: Math.floor(Date.now() / 1000),
        timeNano: Date.now() * 1_000_000,
      },
    });
  }
}

function requireString(value, label) {
  if (typeof value !== 'string' || value.trim() === '') {
    throw pluginError('INVALID_PARAMS', `参数缺失或非法: ${label}`);
  }
  return value;
}

/** 校验 registry server（可省略）；非法时返回 INVALID_PARAMS。 */
function validateRegistryServer(value) {
  if (value === undefined || value === null || value === '') return 'docker.io';
  if (typeof value !== 'string') throw pluginError('INVALID_PARAMS', 'server 不合法', 'server 必须是字符串');
  const server = value.trim();
  if (!server || server.length > 253 || /[\s\x00-\x1f]/.test(server)) {
    throw pluginError('INVALID_PARAMS', 'server 不合法', 'server 不能为空、不能含空白/控制字符，长度需在 1-253 之间');
  }
  return server.toLowerCase() === 'docker.io' ? 'docker.io' : server;
}

function requireArray(value, label) {
  if (!Array.isArray(value)) throw pluginError('INVALID_PARAMS', `参数缺失或非法: ${label}`);
  return value;
}

const handlers = {
  async initialize(params) {
    const versions = params?.host?.protocolVersions;
    if (!Array.isArray(versions) || !versions.includes(PROTOCOL_VERSION)) {
      throw { __rpc: true, code: -32001, message: 'DBX and plugin do not share a protocol version' };
    }
    return {
      protocolVersion: PROTOCOL_VERSION,
      capabilities: ['wsl', 'wslc', 'jobs', 'events'],
      plugin: { id: PLUGIN_ID, version: PLUGIN_VERSION },
    };
  },

  async 'system/info'() {
    return {
      platform: {
        os: 'windows',
        arch: 'amd64',
        hostname: 'QA-MOCK',
        username: 'qa',
        windowsVersion: '10.0.26300.9711',
      },
      wsl: {
        available: true,
        exe: 'C:\\Windows\\System32\\wsl.exe',
        version: {
          wsl: '3.0.3.0',
          kernel: '6.18.54.1-1',
          wslg: '1.0.79',
          msrdc: '1.2.7391',
          direct3d: '1.611.1-81528511',
          dxcore: '10.0.26100.1-240331-1435.ge-release',
          windows: '10.0.26300.9711',
        },
        status: { defaultDistro: 'Debian', defaultVersion: 2, wsl1Supported: false, raw: 'mock' },
        distroCount: state.distros.length,
        runningCount: state.distros.filter((distro) => distro.running).length,
      },
      wslc: {
        available: true,
        exe: 'C:\\Program Files\\WSL\\wslc.exe',
        version: '3.0.3.0',
        client: { version: '3.0.3.0', kernelVersion: '6.18.54.1-1', settingsFile: 'C:\\Users\\qa\\AppData\\Local\\wslc\\settings.yaml' },
        server: { sessionManagerVersion: '3.0.3', sessions: [{ id: 1, name: 'wslc-cli-qa', creatorPid: 1234 }] },
      },
      warnings: [],
    };
  },

  async 'system/wsl/status'() {
    return {
      defaultDistro: 'Debian',
      defaultVersion: 2,
      wsl1Supported: false,
      raw: '默认分发: Debian\n默认版本: 2\n',
    };
  },

  async 'wsl/distros/list'() {
    return {
      distros: state.distros.map((distro) => ({ ...distro })),
      defaultDistro: state.distros.find((distro) => distro.isDefault)?.name ?? '',
      defaultVersion: 2,
      count: state.distros.length,
    };
  },

  async 'wsl/distros/terminate'(params) {
    const name = requireString(params?.name, 'name');
    const distro = state.distros.find((item) => item.name === name);
    if (!distro) throw pluginError('NOT_FOUND', `发行版不存在: ${name}`);
    distro.state = 'Stopped';
    distro.running = false;
    return { name, state: 'Stopped', changed: true };
  },

  async 'wsl/distros/info'(params) {
    const name = requireString(params?.name, 'name');
    const distro = state.distros.find((item) => item.name === name);
    if (!distro) throw pluginError('NOT_FOUND', `发行版不存在: ${name}`);
    return {
      name,
      running: true,
      osRelease: {
        PRETTY_NAME: 'Debian GNU/Linux 13 (trixie)',
        NAME: 'Debian GNU/Linux',
        VERSION_ID: '13',
        ID: 'debian',
      },
      kernel: '6.18.54.1-microsoft-standard-WSL2',
      arch: 'x86_64',
      uid: 1000,
      userName: 'qa',
      defaultShell: '/bin/bash',
      disk: { totalBytes: 1081101176832, usedBytes: 8233324544, availBytes: 1017875496960, usePercent: 1 },
      ipv4: ['172.20.10.5'],
      uptime: 'up 2 minutes',
      wslConf: '[boot]\nsystemd=true\n',
    };
  },

  async 'wsl/distros/exec'(params) {
    const name = requireString(params?.name, 'name');
    const command = requireString(params?.command, 'command');
    return {
      name,
      exitCode: 0,
      stdout: `${command.includes('echo hello') ? 'hello' : 'ok'}\n`,
      stderr: '',
      durationMs: 12,
      truncated: false,
      command: `wsl -d ${name} -e sh -lc '${command}'`,
      started: true,
    };
  },

  async 'wslc/info'() {
    return {
      version: '3.0.3.0',
      client: {
        version: '3.0.3.0',
        kernelVersion: '6.18.54.1-1',
        direct3dVersion: '1.611.1-81528511',
        dxCoreVersion: '10.0.26100.1-240331-1435.ge-release',
        windowsVersion: '10.0.26300.9711',
        settingsFile: 'C:\\Users\\qa\\AppData\\Local\\wslc\\settings.yaml',
      },
      server: {
        sessionManagerVersion: '3.0.3',
        sessions: [{ id: 1, name: 'wslc-cli-qa', creatorPid: 1234 }],
      },
    };
  },

  async 'wslc/networks/list'() {
    return { networks: [...state.networks.values()].map((network) => ({ ...network })) };
  },

  async 'wslc/networks/inspect'(params) {
    const name = requireString(params?.name, 'name');
    const network = state.networks.get(name);
    if (!network) throw pluginError('NOT_FOUND', `网络不存在: ${name}`);
    return { raw: { ...network } };
  },

  async 'wslc/networks/create'(params) {
    const name = requireString(params?.name, 'name');
    if (state.networks.has(name)) throw pluginError('CONFLICT', `网络已存在: ${name}`);
    const network = {
      id: randomBytes(6).toString('hex'),
      name,
      driver: params.driver || 'bridge',
      scope: 'local',
      ipv4: true,
      ipv6: Boolean(params.ipv6),
      internal: Boolean(params.internal),
      createdAt: '2026-10-20 22:50:00 +0000 UTC',
      labels: '',
    };
    state.networks.set(name, network);
    return { id: network.id, name: network.name };
  },

  async 'wslc/networks/remove'(params) {
    const names = requireArray(params?.names, 'names');
    const removed = [];
    for (const name of names) {
      if (state.networks.delete(name)) removed.push(name);
    }
    return { removed, output: removed.join('\n') };
  },

  async 'wslc/volumes/list'() {
    return { volumes: [...state.volumes.values()].map((volume) => ({ ...volume })) };
  },

  async 'wslc/volumes/inspect'(params) {
    const name = requireString(params?.name, 'name');
    const volume = state.volumes.get(name);
    if (!volume) throw pluginError('NOT_FOUND', `卷不存在: ${name}`);
    return { raw: { ...volume } };
  },

  async 'wslc/volumes/create'(params) {
    const name = params?.name && String(params.name).trim() ? String(params.name) : `auto-${randomBytes(4).toString('hex')}`;
    if (state.volumes.has(name)) throw pluginError('CONFLICT', `卷已存在: ${name}`);
    const volume = {
      name,
      driver: 'guest',
      mountpoint: `/var/lib/docker/volumes/${name}/_data`,
      scope: 'local',
      size: 'N/A',
      labels: '',
    };
    state.volumes.set(name, volume);
    return { name };
  },

  async 'wslc/volumes/remove'(params) {
    const names = requireArray(params?.names, 'names');
    const removed = [];
    for (const name of names) {
      if (state.volumes.delete(name)) removed.push(name);
    }
    return { removed, output: removed.join('\n') };
  },

  async 'wslc/images/list'() {
    return { images: state.images.map((image) => ({ ...image })) };
  },

  async 'wslc/images/pull'(params) {
    const ref = requireString(params?.ref, 'ref');
    const job = createJob('wslc/images/pull', params);
    const [repository, tag] = ref.includes(':') ? [ref.slice(0, ref.lastIndexOf(':')), ref.slice(ref.lastIndexOf(':') + 1)] : [ref, 'latest'];
    const exists = state.images.some((image) => image.repository === repository && image.tag === tag);
    const denied = /definitely-not-a-real-image|private-qa/i.test(ref);
    if (denied) {
      setTimeout(() => jobOutput(job, 'stderr', `Error response from daemon: pull access denied for ${repository}, repository does not exist or may require 'docker login': denied: requested access to the resource is denied\n`), 30);
      setTimeout(() => {
        jobEnd(job, {
          ok: false,
          exitCode: 1,
          error: {
            code: 'REGISTRY_AUTH_REQUIRED',
            hint: '该镜像需要登录注册表；请到「镜像 → 注册表凭据」登录后重试',
            detail: `pull access denied for ${repository}, may require 'docker login'`,
          },
        });
      }, 120);
      return { jobId: job.jobId };
    }
    setTimeout(() => jobOutput(job, 'stdout', `Pulling from library/${repository}\n`), 20);
    setTimeout(() => jobOutput(job, 'stdout', `Digest: sha256:${randomBytes(32).toString('hex')}\n`), 60);
    setTimeout(() => {
      if (!exists) {
        state.images.push({
          id: randomBytes(6).toString('hex'),
          repository,
          tag,
          digest: `<none>`,
          createdAt: '2026-10-20 22:50:00 +0800 GMT+8',
          createdSince: 'now',
          size: '7.81MB',
          containers: '0',
          uniqueSize: 'N/A',
        });
      }
      jobEnd(job, { ok: true, exitCode: 0, result: { ref } });
    }, 120);
    return { jobId: job.jobId };
  },

  async 'wslc/registry/login'(params) {
    const username = requireString(params?.username, 'username');
    const password = requireString(params?.password, 'password');
    if (typeof password !== 'string' || password.length > 4096) {
      throw pluginError('INVALID_PARAMS', 'password 不合法', '请检查密码长度');
    }
    const server = validateRegistryServer(params?.server);
    if (/[\r\n\t\x00]/.test(username) || username.length > 256) {
      throw pluginError('INVALID_PARAMS', 'username 不合法', '用户名不能包含控制字符且长度需在 1-256 之间');
    }
    // mock 内置一组“正确凭据”用于验证成功路径；其余一律认证失败（不访问网络）
    if (username === 'qa-valid' && password === 'qa-valid-password') {
      state.logins.set(server, username);
      return { server, username, loggedIn: true, output: 'Login Succeeded' };
    }
    throw pluginError(
      'REGISTRY_AUTH_FAILED',
      '注册表登录失败',
      '请检查用户名与 PAT/密码是否正确',
      'unauthorized: incorrect username or password',
    );
  },

  async 'wslc/registry/logout'(params) {
    const server = validateRegistryServer(params?.server);
    state.logins.delete(server);
    return { server, loggedOut: true, output: `Removing login credentials for ${server}` };
  },

  async 'wslc/images/inspect'(params) {
    const ref = requireString(params?.ref, 'ref');
    const [repository, tag] = ref.includes(':') ? [ref.slice(0, ref.lastIndexOf(':')), ref.slice(ref.lastIndexOf(':') + 1)] : [ref, 'latest'];
    const image = state.images.find((item) => item.repository === repository && item.tag === tag);
    if (!image) throw pluginError('NOT_FOUND', `镜像不存在: ${ref}`);
    return { raw: { ...image } };
  },

  async 'wslc/containers/list'() {
    return { containers: [...state.containers.values()].map((container) => ({ ...container })) };
  },

  async 'wslc/containers/inspect'(params) {
    const id = requireString(params?.id, 'id');
    const container = findContainer(id);
    if (!container) throw pluginError('NOT_FOUND', `容器不存在: ${id}`, '请检查容器名称或 ID', `wslc inspect ${id}`);
    return { raw: { ...container } };
  },

  async 'wslc/containers/run'(params) {
    const image = requireString(params?.image, 'image');
    const name = params?.name && String(params.name).trim() ? String(params.name) : `auto-${randomBytes(4).toString('hex')}`;
    if (state.containers.has(name)) throw pluginError('CONFLICT', `容器已存在: ${name}`);
    const id = containerId();
    const container = {
      id,
      shortId: id.slice(0, 12),
      name,
      image,
      command: JSON.stringify((params.command || []).join(' ')),
      createdAt: '2026-10-20 22:50:00 +0800 GMT+8',
      runningFor: '1 second ago',
      state: 'running',
      status: 'Up 1 second',
      ports: '',
      networks: 'bridge',
      size: '0B',
      health: '',
      platform: { architecture: 'amd64', os: 'linux' },
    };
    state.containers.set(name, container);
    return { id, name };
  },

  async 'wslc/containers/exec'(params) {
    const id = requireString(params?.id, 'id');
    const container = findContainer(id);
    if (!container) throw pluginError('NOT_FOUND', `容器不存在: ${id}`);
    const command = requireArray(params?.command, 'command');
    return {
      exitCode: 0,
      stdout: command.join(' ').includes('echo') ? 'hi\n' : `${command.join(' ')}\n`,
      stderr: '',
      durationMs: 9,
      truncated: false,
      command: `wslc exec ${id} ${command.join(' ')}`,
    };
  },

  async 'wslc/containers/logs'(params) {
    const id = requireString(params?.id, 'id');
    const container = findContainer(id);
    if (!container) throw pluginError('NOT_FOUND', `容器不存在: ${id}`);
    if (!params?.follow) {
      return {
        exitCode: 0,
        stdout: container.name.startsWith('qa-job') ? 'tick\n' : '',
        stderr: '',
        durationMs: 6,
        truncated: false,
        command: `wslc logs ${id}`,
      };
    }
    const job = createJob('wslc/containers/logs', params);
    setTimeout(() => jobOutput(job, 'stdout', 'tick\n'), 30);
    return { jobId: job.jobId };
  },

  async 'wslc/containers/stats'() {
    const stats = [...state.containers.values()]
      .filter((container) => container.state === 'running')
      .map((container) => ({
        id: container.id,
        shortId: container.shortId,
        name: container.name,
        cpuPerc: '0.00%',
        memUsage: '1.461MiB / 31.24GiB',
        memPerc: '0.00%',
        netIO: '1.04kB / 0B',
        blockIO: '0B / 0B',
        pids: 1,
      }));
    return { stats };
  },

  async 'wslc/containers/stop'(params) {
    const id = requireString(params?.id, 'id');
    const container = findContainer(id);
    if (!container) throw pluginError('NOT_FOUND', `容器不存在: ${id}`);
    container.state = 'exited';
    container.status = 'Exited (0) 1 second ago';
    emitWSLCContainerEvent('die', container.name);
    return { id: container.name, action: 'stop', output: `${container.name}\n`, exitCode: 0 };
  },

  async 'wslc/containers/remove'(params) {
    const id = requireString(params?.id, 'id');
    const container = findContainer(id);
    if (!container) throw pluginError('NOT_FOUND', `容器不存在: ${id}`);
    state.containers.delete(container.name);
    return { id: container.name, action: 'remove', output: `${container.name}\n`, exitCode: 0 };
  },

  async 'wslc/events/start'(params) {
    const job = createJob('wslc/events/start', params);
    return { jobId: job.jobId };
  },

  async 'wslc/events/stop'(params) {
    const jobId = requireString(params?.jobId, 'jobId');
    const job = state.jobs.get(jobId);
    if (!job) throw pluginError('NOT_FOUND', `任务不存在: ${jobId}`);
    if (job.status === 'running') {
      job.cancelled = true;
      jobEnd(job, {
        ok: false,
        exitCode: -1,
        error: { code: 'PROCESS_FAILED', hint: '事件订阅已停止', detail: 'events/stop' },
      });
    }
    return { stopped: true };
  },

  async 'system/config/read'() {
    return {
      wslconfig: { path: 'C:\\Users\\qa\\.wslconfig', exists: false, content: '' },
      wslcSettings: { path: 'C:\\Users\\qa\\AppData\\Local\\wslc\\settings.yaml', exists: false, content: '' },
    };
  },

  async 'console/presets'() {
    return {
      presets: [
        { id: 'wsl-list', label: 'wsl -l -v', tool: 'wsl', args: ['-l', '-v'] },
        { id: 'wslc-list', label: 'wslc list --all', tool: 'wslc', args: ['list', '--all'] },
      ],
    };
  },

  async 'jobs/get'(params) {
    const jobId = requireString(params?.jobId, 'jobId');
    const job = state.jobs.get(jobId);
    if (!job) throw pluginError('NOT_FOUND', `任务不存在: ${jobId}`);
    return {
      jobId: job.jobId,
      method: job.method,
      status: job.status,
      startedAt: job.startedAt,
      durationMs: (job.finishedAt ?? Date.now()) - job.startedAt,
      exitCode: job.exitCode,
      error: job.error,
      lines: job.lines.map((line) => ({ ...line })),
    };
  },

  async 'jobs/list'() {
    return {
      jobs: [...state.jobs.values()]
        .filter((job) => job.status === 'running')
        .map((job) => ({ jobId: job.jobId, method: job.method, startedAt: job.startedAt })),
    };
  },

  async 'jobs/cancel'(params) {
    const jobId = requireString(params?.jobId, 'jobId');
    const job = state.jobs.get(jobId);
    if (!job) throw pluginError('NOT_FOUND', `任务不存在: ${jobId}`);
    if (job.status === 'running') {
      job.cancelled = true;
      jobEnd(job, {
        ok: false,
        exitCode: -1,
        error: { code: 'PROCESS_FAILED', hint: '任务已取消', detail: 'cancelled by user' },
      });
    }
    return { jobId, cancelled: true };
  },

  async 'console/run'(params) {
    const tool = requireString(params?.tool, 'tool');
    if (tool !== 'wsl' && tool !== 'wslc') throw pluginError('INVALID_PARAMS', `不支持的 tool: ${tool}`);
    const args = requireArray(params?.args, 'args');
    return {
      tool,
      exitCode: 0,
      stdout: '  NAME      STATE           VERSION\n* Debian    Stopped         2\n',
      stderr: '',
      durationMs: 11,
      truncated: false,
      command: `${tool} ${args.join(' ')}`,
    };
  },
};

function findContainer(id) {
  if (state.containers.has(id)) return state.containers.get(id);
  for (const container of state.containers.values()) {
    if (container.id === id || container.shortId === id) return container;
  }
  return null;
}

// ---------------------------------------------------------------------------
// 主循环
// ---------------------------------------------------------------------------

let buffer = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', (chunk) => {
  buffer += chunk;
  let index;
  while ((index = buffer.indexOf('\n')) >= 0) {
    const line = buffer.slice(0, index).replace(/\r$/, '').trim();
    buffer = buffer.slice(index + 1);
    if (!line) continue;
    void handleLine(line);
  }
});

async function handleLine(line) {
  let request;
  try {
    request = JSON.parse(line);
  } catch {
    process.stderr.write(`[mock-sidecar] 无法解析请求: ${line.slice(0, 120)}\n`);
    return;
  }
  const method = request.method;
  const id = request.id;
  if (method === 'plugin/initialize') {
    try {
      const result = await handlers.initialize(request.params);
      if (id !== undefined) rpcResult(id, result);
    } catch (error) {
      if (id !== undefined) {
        if (error && error.__rpc) rpcError(id, error.code, error.message);
        else rpcError(id, -32603, String(error?.message || error));
      }
    }
    return;
  }
  const handler = handlers[method];
  if (!handler) {
    if (id !== undefined) rpcError(id, -32601, `Method not found: ${method}`);
    return;
  }
  try {
    await delay(2);
    const result = await handler(request.params ?? {});
    if (id !== undefined) rpcResult(id, result);
  } catch (error) {
    if (id === undefined) return;
    if (error && error.code && error.message && typeof error.code === 'string') {
      rpcError(id, -32000, error.message, error);
    } else {
      rpcError(id, -32603, String(error?.message || error));
    }
  }
}

process.stdin.on('end', () => {
  for (const job of state.jobs.values()) {
    for (const timer of job.timers) clearTimeout(timer);
  }
  process.exit(0);
});

process.on('SIGTERM', () => process.exit(0));
process.on('SIGINT', () => process.exit(0));
