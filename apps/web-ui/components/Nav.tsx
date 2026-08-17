"use client";

import { useEffect, useState } from "react";
import { useRouter, usePathname } from "next/navigation";
import { getToken, getUsername, clearToken } from "@/lib/api";

export default function Nav() {
  const router = useRouter();
  const pathname = usePathname();
  const [loggedIn, setLoggedIn] = useState(false);
  const [username, setUsername] = useState<string | null>(null);

  // Re-check the token on every route change, so a login or logout redirect
  // updates the nav.
  //
  // No `storage` event listener here: the session lives in sessionStorage
  // (clearToken/setToken in lib/api.ts), and the storage event only fires
  // for localStorage, and only in *other* tabs — sessionStorage is per-tab
  // to begin with, so there is no cross-tab state to react to. A listener
  // for it is dead code that looks like cross-tab sync but never runs.
  useEffect(() => {
    setLoggedIn(!!getToken());
    setUsername(getUsername());
  }, [pathname]);

  const handleLogout = () => {
    clearToken();
    setLoggedIn(false);
    setUsername(null);
    router.push("/login");
  };

  const linkClass =
    "px-3 py-1.5 rounded-lg text-slate-700 hover:bg-white/70 transition";

  return (
    <nav className="flex items-center gap-1 text-sm">
      {loggedIn ? (
        <>
          <a href="/orders" className={linkClass}>
            Shop
          </a>
          <a href="/pay" className={linkClass}>
            Pay
          </a>
          {username && (
            <span className="px-3 py-1.5 text-xs text-slate-500">
              👋 {username}
            </span>
          )}
          <button
            onClick={handleLogout}
            className="px-3 py-1.5 rounded-lg text-rose-700 hover:bg-rose-50 transition"
          >
            Logout
          </button>
        </>
      ) : (
        <a href="/login" className={linkClass}>
          Login
        </a>
      )}
    </nav>
  );
}
