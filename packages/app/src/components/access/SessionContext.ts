import { createContext, useContext } from "react";
import type { Account } from "../../api/types.ts";

interface SharedAccount {
  account: Account;
  refresh: () => void;
  /** The server changed or revoked the browser session. */
  sessionChanged: () => void;
}

export const SharedAccountContext = createContext<SharedAccount | null>(null);
export const useSharedAccount = () => useContext(SharedAccountContext);
export const useIsAdmin = () => useSharedAccount()?.account.role === "admin";
