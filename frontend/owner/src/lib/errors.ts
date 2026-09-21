import { status } from '@grpc/grpc-js';

/** Whether a gRPC failure is the API refusing the caller's credentials. */
export function isUnauthenticated(err: unknown): boolean {
  return typeof err === 'object' && err !== null && (err as { code?: number }).code === status.UNAUTHENTICATED;
}

/** A message safe to show a person. */
export function messageOf(err: unknown, fallback: string): string {
  if (err instanceof Error) {
    if (err.message.includes('ECONNREFUSED') || err.message.includes('UNAVAILABLE')) {
      return 'Cannot reach the Fijo API. Check GRPC_API_SERVER_URL.';
    }
    const details = (err as { details?: string }).details;
    return details && details.length > 0 ? details : fallback;
  }
  return fallback;
}
