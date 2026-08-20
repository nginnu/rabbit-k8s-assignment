const CLUBS = [
  { slug: "arsenal", name: "Arsenal" },
  { slug: "aston-villa", name: "Aston Villa" },
  { slug: "bournemouth", name: "Bournemouth" },
  { slug: "chelsea", name: "Chelsea" },
  { slug: "crystal-palace", name: "Crystal Palace" },
  { slug: "liverpool", name: "Liverpool" },
  { slug: "manchester-city", name: "Manchester City" },
  { slug: "manchester-united", name: "Manchester United" },
  { slug: "newcastle-united", name: "Newcastle United" },
  { slug: "tottenham", name: "Tottenham Hotspur" },
];

export default function Home() {
  // Duplicate the list so the marquee track scrolls seamlessly (no empty gap).
  const loop = [...CLUBS, ...CLUBS];

  return (
    <main className="relative flex h-screen w-screen items-center justify-center overflow-hidden bg-white">
      {/* ── Full-screen club marquee ─────────────────────────────── */}
      <div className="club-marquee club-marquee--fullscreen w-full">
        <div className="club-marquee__viewport">
          <ul className="club-marquee__track">
            {loop.map((club, i) => (
              <li key={`${club.slug}-${i}`} className="club-marquee__item">
                <img
                  src={`/clubs/${club.slug}.svg`}
                  alt={`${club.name} crest`}
                  loading="lazy"
                />
                <span>{club.name}</span>
              </li>
            ))}
          </ul>
        </div>
      </div>

      {/* ── Centre overlay: title + entry ────────────────────────── */}
      <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center gap-8 px-6 text-center">
        <div className="pointer-events-auto rounded-3xl bg-white/70 px-8 py-10 backdrop-blur-md ring-1 ring-slate-200">
          <div className="inline-flex items-center gap-2 text-xs font-bold uppercase tracking-[0.22em] text-sky-700">
            <span className="text-base">⚽</span> Season 2024/25
          </div>
          <h1 className="mt-5 text-4xl font-black leading-[0.95] tracking-tight text-slate-900 md:text-6xl">
            Premier League
            <span className="mt-2 block text-sky-600">Store</span>
          </h1>
          <p className="mt-5 max-w-md text-sm leading-7 text-slate-600">
            Shop the latest kits from all twenty clubs. Sign in to browse,
            order, and check out.
          </p>
          <a
            href="/login"
            className="btn-primary pointer-events-auto mt-7 inline-flex px-8 py-3 text-base"
          >
            Enter Store →
          </a>
        </div>
      </div>
    </main>
  );
}
