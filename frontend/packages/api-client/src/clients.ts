/**
 * gRPC clients for the Fijo API, promisified, with per-call auth in metadata rather than per-client
 * credentials — the shape DDB's consumer app uses. Three services: platform's sign-in (password
 * login and the refresh exchange), Fijo's owner auth (passkeys, reset, sign-out) and Fijo's owners
 * (who am I, my time zone).
 */
import * as grpc from '@grpc/grpc-js';
import type { Metadata } from '@grpc/grpc-js';
import {
  SignInServiceClient,
  type ExchangeRefreshTokenRequest,
  type ExchangeRefreshTokenResponse,
  type LoginForTokenRequest,
  type LoginForTokenResponse,
} from './generated/primandproper/platform/signin/v1/signin.js';
import {
  OwnerAuthServiceClient,
  type ArchivePasskeyRequest,
  type ArchivePasskeyResponse,
  type BeginPasskeyLoginRequest,
  type BeginPasskeyLoginResponse,
  type BeginPasskeyRegistrationRequest,
  type BeginPasskeyRegistrationResponse,
  type CompletePasswordResetRequest,
  type CompletePasswordResetResponse,
  type FinishPasskeyLoginRequest,
  type FinishPasskeyLoginResponse,
  type FinishPasskeyRegistrationRequest,
  type FinishPasskeyRegistrationResponse,
  type ListPasskeysRequest,
  type ListPasskeysResponse,
  type RequestPasswordResetRequest,
  type RequestPasswordResetResponse,
  type SignOutRequest,
  type SignOutResponse,
} from './generated/fijo/v1/owner_auth.js';
import {
  OwnersServiceClient,
  type GetSelfRequest,
  type GetSelfResponse,
  type SetTimeZoneRequest,
  type SetTimeZoneResponse,
} from './generated/fijo/v1/owners.js';

export interface ClientConfig {
  serverUrl: string;
  insecure?: boolean;
}

type Unary<Req, Res> = (
  req: Req,
  metadata: Metadata,
  callback: (err: grpc.ServiceError | null, res: Res) => void,
) => grpc.ClientUnaryCall;

function promisify<Req, Res>(call: Unary<Req, Res>): (req: Req, metadata: Metadata) => Promise<Res> {
  return (req, metadata) =>
    new Promise((resolve, reject) => {
      call(req, metadata, (err, res) => {
        if (err) reject(err);
        else if (res) resolve(res);
        else reject(new Error('no response'));
      });
    });
}

function bearer(token: string): Metadata {
  const m = new grpc.Metadata();
  m.add('authorization', `Bearer ${token}`);
  return m;
}

const anonymous = new grpc.Metadata();

export function createClients(config: ClientConfig) {
  const credentials = config.insecure ? grpc.credentials.createInsecure() : grpc.credentials.createSsl();

  const signInClient = new SignInServiceClient(config.serverUrl, credentials);
  const ownerAuthClient = new OwnerAuthServiceClient(config.serverUrl, credentials);
  const ownersClient = new OwnersServiceClient(config.serverUrl, credentials);

  // The generated clients overload each method, so the pair is spelled out rather than inferred.
  const loginForToken = promisify<LoginForTokenRequest, LoginForTokenResponse>(
    signInClient.loginForToken.bind(signInClient),
  );
  const exchangeRefreshToken = promisify<ExchangeRefreshTokenRequest, ExchangeRefreshTokenResponse>(
    signInClient.exchangeRefreshToken.bind(signInClient),
  );

  const beginPasskeyRegistration = promisify<BeginPasskeyRegistrationRequest, BeginPasskeyRegistrationResponse>(
    ownerAuthClient.beginPasskeyRegistration.bind(ownerAuthClient),
  );
  const finishPasskeyRegistration = promisify<FinishPasskeyRegistrationRequest, FinishPasskeyRegistrationResponse>(
    ownerAuthClient.finishPasskeyRegistration.bind(ownerAuthClient),
  );
  const listPasskeys = promisify<ListPasskeysRequest, ListPasskeysResponse>(
    ownerAuthClient.listPasskeys.bind(ownerAuthClient),
  );
  const archivePasskey = promisify<ArchivePasskeyRequest, ArchivePasskeyResponse>(
    ownerAuthClient.archivePasskey.bind(ownerAuthClient),
  );
  const beginPasskeyLogin = promisify<BeginPasskeyLoginRequest, BeginPasskeyLoginResponse>(
    ownerAuthClient.beginPasskeyLogin.bind(ownerAuthClient),
  );
  const finishPasskeyLogin = promisify<FinishPasskeyLoginRequest, FinishPasskeyLoginResponse>(
    ownerAuthClient.finishPasskeyLogin.bind(ownerAuthClient),
  );
  const requestPasswordReset = promisify<RequestPasswordResetRequest, RequestPasswordResetResponse>(
    ownerAuthClient.requestPasswordReset.bind(ownerAuthClient),
  );
  const completePasswordReset = promisify<CompletePasswordResetRequest, CompletePasswordResetResponse>(
    ownerAuthClient.completePasswordReset.bind(ownerAuthClient),
  );
  const signOut = promisify<SignOutRequest, SignOutResponse>(ownerAuthClient.signOut.bind(ownerAuthClient));

  const getSelf = promisify<GetSelfRequest, GetSelfResponse>(ownersClient.getSelf.bind(ownersClient));
  const setTimeZone = promisify<SetTimeZoneRequest, SetTimeZoneResponse>(ownersClient.setTimeZone.bind(ownersClient));

  return {
    signIn: {
      loginForToken: (username: string, password: string, totpCode = ''): Promise<LoginForTokenResponse> =>
        loginForToken(
          { credentials: { username, emailAddress: '', password, totpCode, activeAccountId: '' } },
          anonymous,
        ),
      exchangeRefreshToken: (refreshToken: string): Promise<ExchangeRefreshTokenResponse> =>
        exchangeRefreshToken({ refreshToken }, anonymous),
    },
    ownerAuth: {
      beginPasskeyRegistration: (token: string): Promise<BeginPasskeyRegistrationResponse> =>
        beginPasskeyRegistration({}, bearer(token)),
      finishPasskeyRegistration: (
        token: string,
        attestationJson: Uint8Array,
        friendlyName: string,
      ): Promise<FinishPasskeyRegistrationResponse> =>
        finishPasskeyRegistration({ attestationJson, friendlyName }, bearer(token)),
      listPasskeys: (token: string): Promise<ListPasskeysResponse> => listPasskeys({}, bearer(token)),
      archivePasskey: (token: string, id: string): Promise<ArchivePasskeyResponse> =>
        archivePasskey({ id }, bearer(token)),
      beginPasskeyLogin: (username: string): Promise<BeginPasskeyLoginResponse> =>
        beginPasskeyLogin({ username }, anonymous),
      finishPasskeyLogin: (username: string, assertionJson: Uint8Array): Promise<FinishPasskeyLoginResponse> =>
        finishPasskeyLogin({ username, assertionJson }, anonymous),
      requestPasswordReset: (emailAddress: string): Promise<RequestPasswordResetResponse> =>
        requestPasswordReset({ emailAddress }, anonymous),
      completePasswordReset: (resetToken: string, newPassword: string): Promise<CompletePasswordResetResponse> =>
        completePasswordReset({ token: resetToken, newPassword }, anonymous),
      signOut: (token: string): Promise<SignOutResponse> => signOut({}, bearer(token)),
    },
    owners: {
      getSelf: (token: string): Promise<GetSelfResponse> => getSelf({}, bearer(token)),
      setTimeZone: (token: string, timeZone: string): Promise<SetTimeZoneResponse> =>
        setTimeZone({ timeZone }, bearer(token)),
    },
  };
}

export type Clients = ReturnType<typeof createClients>;
