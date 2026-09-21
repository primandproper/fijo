import { env } from '$env/dynamic/private';
import { createClients } from '@fijo/api-client';

export const api = createClients({
  serverUrl: env.GRPC_API_SERVER_URL ?? 'localhost:50051',
  insecure: env.GRPC_INSECURE === 'true',
});
