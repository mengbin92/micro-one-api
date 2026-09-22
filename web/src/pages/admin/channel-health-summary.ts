export interface ChannelHealthInput {
  healthStatus?: string;
  health_status?: string;
  status?: number | string;
}

export function channelHealthStatus(channel: ChannelHealthInput) {
  if (channel.status !== undefined && channel.status !== null && Number(channel.status) !== 1) return 'unavailable';
  const health = channel.healthStatus || channel.health_status;
  return health === 'healthy' || health === 'degraded' || health === 'unavailable' ? health : 'unknown';
}

export function summarizeChannelHealth<T extends ChannelHealthInput>(channels: T[] = []) {
  const unhealthy = channels.filter((channel) => {
    const status = channelHealthStatus(channel);
    return status === 'unavailable' || status === 'degraded';
  });
  const unavailable = unhealthy.filter((channel) => channelHealthStatus(channel) === 'unavailable');
  const degraded = unhealthy.filter((channel) => channelHealthStatus(channel) === 'degraded');
  const unknown = channels.filter((channel) => channelHealthStatus(channel) === 'unknown');
  const primary = unavailable[0] ?? degraded[0] ?? null;
  return {
    unhealthy,
    unavailable,
    degraded,
    unknown,
    primary,
  };
}
