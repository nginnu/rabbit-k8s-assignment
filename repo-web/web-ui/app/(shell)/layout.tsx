import Nav from "@/components/Nav";

export default function ShellLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <div className="min-h-screen px-5 py-5 md:px-10 md:py-8">
      <header className="mb-6 flex items-center justify-between border-b border-slate-200 px-1 pb-4 animate-fade-in">
        <a href="/" className="flex items-center gap-3 group">
          <span className="grid h-10 w-10 place-items-center rounded-xl bg-slate-900 p-1.5 shadow-glass-sm transition group-hover:scale-105">
            <img
              src="/premier-league.svg"
              alt=""
              className="h-full w-full object-contain"
            />
          </span>
          <span className="flex flex-col leading-tight">
            <span className="font-bold text-slate-800 text-lg tracking-tight">
              Premier League
            </span>
            <span className="text-xs text-sky-700/80">
              Premier League Store
            </span>
          </span>
        </a>

        <Nav />
      </header>

      <main className="animate-fade-in">{children}</main>
    </div>
  );
}
