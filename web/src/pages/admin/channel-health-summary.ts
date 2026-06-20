export interface ChannelHealthInput {
  healthStatus?: string;
  health_status?: string;
}

function channelHealthStatus(channel: ChannelHealthInput) {
  return channel.healthStatus || channel.health_status || 'healthy';
}

export function summarizeChannelHealth<T extends ChannelHealthInput>(channels: T[] = []) {
  const unhealthy = channels.filter((channel) => {
    const status = channelHealthStatus(channel);
    return status === 'unavailable' || status === 'degraded';
  });
  const unavailable = unhealthy.filter((channel) => channelHealthStatus(channel) === 'unavailable');
  const degraded = unhealthy.filter((channel) => channelHealthStatus(channel) === 'degraded');
  const primary = unavailable[0] ?? degraded[0] ?? null;
  return {
    unhealthy,
    unavailable,
    degraded,
    primary,
  };
}
