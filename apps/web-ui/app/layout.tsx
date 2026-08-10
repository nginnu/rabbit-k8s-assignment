import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Premier League Store",
  description: "Sign in, browse jerseys, place an order",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body className="bg-white font-sans text-slate-800 antialiased">
        {children}
      </body>
    </html>
  );
}
