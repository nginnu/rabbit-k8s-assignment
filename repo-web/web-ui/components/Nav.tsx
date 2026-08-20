"use client";

import { useEffect, useState } from "react";
import { useRouter, usePathname } from "next/navigation";
import { me, logout } from "@/lib/api";

export default function Nav() {
  const router = useRouter();
  const pathname = usePathname();
  const [loggedIn, setLoggedIn] = useState(false);
  const [username, setUsername] = useState<string | null>(null);

  // Re-check the session on every route change, so a login or logout
  // redirect updates the nav. The token lives in an HttpOnly cookie, so the
  // client asks the BFF who it is rather than reading storage — and there is
  // no cross-tab event to listen for either way.
  useEffect(() => {
    me().then((res) => {
      setLoggedIn(res.ok);
      setUsername(res.ok && "username" in res.data ? res.data.username : null);
    });
  }, [pathname]);

  const handleLogout = async () => {
    await logout();
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
