/**
 * gRPC clients for the Fijo API, promisified, with per-call auth in metadata rather than per-client
 * credentials — the shape DDB's consumer app uses. The services: platform's sign-in (password
 * login and the refresh exchange), Fijo's owner auth (passkeys, reset, sign-out), Fijo's owners
 * (who am I, my time zone), Fijo's schedule (the grid and the commands that change it) and Fijo's
 * roster (the studio's clients).
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
import {
  ScheduleServiceClient,
  type ArchiveOfferingResponse,
  type Canceller,
  type CloseStudioResponse,
  type CreateOfferingResponse,
  type CreateSeriesResponse,
  type EndSeriesResponse,
  type GetScheduleSettingsResponse,
  type ListClosuresResponse,
  type ListHistoryResponse,
  type ListOccurrencesResponse,
  type ListOfferingsResponse,
  type ListSeriesResponse,
  type Offering,
  type OccurrenceResponse,
  type ReopenStudioResponse,
  type ScheduleSettings,
  type Series,
  type SetScheduleSettingsResponse,
  type UpdateOfferingResponse,
  type UpdateSeriesResponse,
} from './generated/fijo/v1/schedule.js';
import {
  RosterServiceClient,
  type ArchiveHouseholdResponse,
  type Attendee,
  type Contact,
  type Household,
  type HouseholdResponse,
  type ListHouseholdsResponse,
  type NewContact,
} from './generated/fijo/v1/roster.js';

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
  const scheduleClient = new ScheduleServiceClient(config.serverUrl, credentials);
  const rosterClient = new RosterServiceClient(config.serverUrl, credentials);

  // Every schedule call is the caller's own studio, so each is the request and a bearer token.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const sched = <Req, Res>(method: (...args: any[]) => grpc.ClientUnaryCall) => {
    const call = promisify<Req, Res>(method.bind(scheduleClient) as Unary<Req, Res>);
    return (token: string, req: Req): Promise<Res> => call(req, bearer(token));
  };

  // The roster's calls have the same shape.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const rost = <Req, Res>(method: (...args: any[]) => grpc.ClientUnaryCall) => {
    const call = promisify<Req, Res>(method.bind(rosterClient) as Unary<Req, Res>);
    return (token: string, req: Req): Promise<Res> => call(req, bearer(token));
  };

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
    schedule: {
      getSettings: sched<object, GetScheduleSettingsResponse>(scheduleClient.getScheduleSettings),
      setSettings: sched<{ settings: ScheduleSettings }, SetScheduleSettingsResponse>(
        scheduleClient.setScheduleSettings,
      ),
      listOfferings: sched<{ includeArchived: boolean }, ListOfferingsResponse>(scheduleClient.listOfferings),
      createOffering: sched<{ offering: Offering }, CreateOfferingResponse>(scheduleClient.createOffering),
      updateOffering: sched<{ offering: Offering }, UpdateOfferingResponse>(scheduleClient.updateOffering),
      archiveOffering: sched<{ id: string }, ArchiveOfferingResponse>(scheduleClient.archiveOffering),
      listSeries: sched<{ includeArchived: boolean }, ListSeriesResponse>(scheduleClient.listSeries),
      createSeries: sched<{ series: Series }, CreateSeriesResponse>(scheduleClient.createSeries),
      updateSeries: sched<{ series: Series }, UpdateSeriesResponse>(scheduleClient.updateSeries),
      endSeries: sched<{ id: string; endsOn: string; reason: string }, EndSeriesResponse>(scheduleClient.endSeries),
      listOccurrences: sched<{ from: Date; until: Date }, ListOccurrencesResponse>(scheduleClient.listOccurrences),
      cancelOccurrence: sched<{ id: string; cancelledBy: Canceller; reason: string }, OccurrenceResponse>(
        scheduleClient.cancelOccurrence,
      ),
      completeOccurrence: sched<{ id: string; reason: string }, OccurrenceResponse>(scheduleClient.completeOccurrence),
      noShowOccurrence: sched<{ id: string; reason: string }, OccurrenceResponse>(scheduleClient.noShowOccurrence),
      revertOccurrence: sched<{ id: string; reason: string }, OccurrenceResponse>(scheduleClient.revertOccurrence),
      moveOccurrence: sched<{ id: string; startsAt: Date; reason: string }, OccurrenceResponse>(
        scheduleClient.moveOccurrence,
      ),
      scheduleMakeUp: sched<{ cancelledOccurrenceId: string; startsAt: Date; reason: string }, OccurrenceResponse>(
        scheduleClient.scheduleMakeUp,
      ),
      reassignOccurrence: sched<{ id: string; staffUserIds: string[] }, OccurrenceResponse>(
        scheduleClient.reassignOccurrence,
      ),
      listClosures: sched<object, ListClosuresResponse>(scheduleClient.listClosures),
      closeStudio: sched<{ startsOn: string; endsOn: string; reason: string }, CloseStudioResponse>(
        scheduleClient.closeStudio,
      ),
      reopenStudio: sched<{ id: string }, ReopenStudioResponse>(scheduleClient.reopenStudio),
      listHistory: sched<{ resourceType: string; resourceId: string; limit: number }, ListHistoryResponse>(
        scheduleClient.listHistory,
      ),
    },
    roster: {
      listHouseholds: rost<{ includeArchived: boolean }, ListHouseholdsResponse>(rosterClient.listHouseholds),
      getHousehold: rost<{ id: string }, HouseholdResponse>(rosterClient.getHousehold),
      createHousehold: rost<{ household: Household; contacts: NewContact[]; attendees: Attendee[] }, HouseholdResponse>(
        rosterClient.createHousehold,
      ),
      updateHousehold: rost<{ household: Household }, HouseholdResponse>(rosterClient.updateHousehold),
      archiveHousehold: rost<{ id: string }, ArchiveHouseholdResponse>(rosterClient.archiveHousehold),
      createContact: rost<{ contact: Contact; attends: boolean }, HouseholdResponse>(rosterClient.createContact),
      updateContact: rost<{ contact: Contact }, HouseholdResponse>(rosterClient.updateContact),
      archiveContact: rost<{ id: string }, HouseholdResponse>(rosterClient.archiveContact),
      createAttendee: rost<{ attendee: Attendee }, HouseholdResponse>(rosterClient.createAttendee),
      updateAttendee: rost<{ attendee: Attendee }, HouseholdResponse>(rosterClient.updateAttendee),
      archiveAttendee: rost<{ id: string }, HouseholdResponse>(rosterClient.archiveAttendee),
    },
  };
}

export type Clients = ReturnType<typeof createClients>;
