/**
 * JerseyArt — inline SVG mock-up of a football jersey, coloured per team.
 * Avoids external image dependencies; looks crisp at any size.
 */

type TeamStyle = {
  body: string;      // main body colour
  sleeves: string;   // sleeves / trim
  accent: string;    // stripe / secondary detail
  collar: string;
  number: string;    // number / text colour
  badge: string;     // 3-letter club badge text
  gradient?: [string, string]; // optional subtle gradient overlay
};

const STYLES: Record<string, TeamStyle> = {
  "LIV-H-24": {
    body: "#C8102E", sleeves: "#A00E23", accent: "#F6EB61",
    collar: "#1B1B1B", number: "#FFFFFF", badge: "LIV",
  },
  "MCI-H-24": {
    body: "#6CABDD", sleeves: "#4F8FC0", accent: "#FFFFFF",
    collar: "#0B2545", number: "#FFFFFF", badge: "MCI",
  },
  "ARS-H-24": {
    body: "#EF0107", sleeves: "#FFFFFF", accent: "#063672",
    collar: "#063672", number: "#FFFFFF", badge: "ARS",
  },
  "MUN-H-24": {
    body: "#DA291C", sleeves: "#B21E13", accent: "#FBE122",
    collar: "#000000", number: "#FBE122", badge: "MUN",
  },
  "CHE-H-24": {
    body: "#034694", sleeves: "#012A5C", accent: "#DBA111",
    collar: "#DBA111", number: "#FFFFFF", badge: "CHE",
  },
  "TOT-H-24": {
    body: "#FFFFFF", sleeves: "#F1F1F1", accent: "#132257",
    collar: "#132257", number: "#132257", badge: "TOT",
  },
};

const FALLBACK: TeamStyle = {
  body: "#38bdf8", sleeves: "#0ea5e9", accent: "#FFFFFF",
  collar: "#0c4a6e", number: "#FFFFFF", badge: "???",
};

export default function JerseyArt({
  productId,
  className = "",
}: {
  productId: string;
  className?: string;
}) {
  const s = STYLES[productId] ?? FALLBACK;
  const gradId = `g-${productId}`;

  return (
    <svg
      viewBox="0 0 240 240"
      xmlns="http://www.w3.org/2000/svg"
      className={className}
      aria-label={`${s.badge} jersey illustration`}
    >
      <defs>
        <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%"  stopColor="#ffffff" stopOpacity="0.25" />
          <stop offset="60%" stopColor="#ffffff" stopOpacity="0" />
          <stop offset="100%" stopColor="#000000" stopOpacity="0.18" />
        </linearGradient>
        <filter id="soft" x="-10%" y="-10%" width="120%" height="120%">
          <feGaussianBlur stdDeviation="0.6" />
        </filter>
      </defs>

      {/* soft ground shadow */}
      <ellipse cx="120" cy="222" rx="70" ry="6" fill="#0c4a6e" opacity="0.15" />

      {/* jersey body: neckline notch + sleeves silhouette */}
      <path
        d="
          M70 40
          L100 28
          C108 40 132 40 140 28
          L170 40
          L205 70
          L185 100
          L170 90
          L170 200
          C170 208 165 212 158 212
          L82 212
          C75 212 70 208 70 200
          L70 90
          L55 100
          L35 70
          Z"
        fill={s.body}
        stroke={s.collar}
        strokeOpacity="0.25"
        strokeWidth="1.5"
      />

      {/* sleeves overlay (slightly darker) */}
      <path
        d="M70 40 L35 70 L55 100 L70 90 Z"
        fill={s.sleeves}
      />
      <path
        d="M170 40 L205 70 L185 100 L170 90 Z"
        fill={s.sleeves}
      />

      {/* centre vertical stripe (accent, for Arsenal/Chelsea/Spurs look) */}
      {["ARS-H-24", "TOT-H-24"].includes(productId) && (
        <rect x="115" y="44" width="10" height="168" fill={s.accent} opacity="0.35" />
      )}

      {/* collar V */}
      <path
        d="M100 28 L120 50 L140 28"
        fill="none"
        stroke={s.collar}
        strokeWidth="4"
        strokeLinecap="round"
        strokeLinejoin="round"
      />

      {/* hem accent line */}
      <rect x="70" y="198" width="100" height="4" fill={s.accent} opacity="0.9" />

      {/* sponsor / number */}
      <text
        x="120"
        y="140"
        textAnchor="middle"
        fontFamily="Inter, system-ui, sans-serif"
        fontWeight="800"
        fontSize="64"
        fill={s.number}
        opacity="0.9"
      >
        10
      </text>

      {/* club badge box (left-chest) */}
      <g transform="translate(88, 70)">
        <rect width="28" height="30" rx="4" fill="#ffffff" opacity="0.85" />
        <text
          x="14" y="20" textAnchor="middle"
          fontFamily="Inter, system-ui, sans-serif"
          fontWeight="800" fontSize="11"
          fill={s.collar}
        >
          {s.badge}
        </text>
      </g>

      {/* gloss overlay */}
      <path
        d="
          M70 40
          L100 28
          C108 40 132 40 140 28
          L170 40
          L205 70
          L185 100
          L170 90
          L170 200
          C170 208 165 212 158 212
          L82 212
          C75 212 70 208 70 200
          L70 90
          L55 100
          L35 70
          Z"
        fill={`url(#${gradId})`}
        filter="url(#soft)"
      />
    </svg>
  );
}
