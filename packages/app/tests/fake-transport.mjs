import { createApi } from "../src/api/client.ts";

/**
 * API client over a fake transport: `respond(path, init)` returns the Response.
 * `subscriptions` records every stream opened, with its handlers.
 */
export function fakeApi(respond = () => Response.json({})) {
  const subscriptions = [];
  const transport = {
    request: async (path, init) => respond(path, init),
    assetUrl: (path) => `asset:${path}`,
    subscribe: (path, handlers) => {
      const sub = { path, handlers, closed: false, close: () => (sub.closed = true) };
      subscriptions.push(sub);
      return sub;
    },
  };
  return { api: createApi(transport), subscriptions };
}
