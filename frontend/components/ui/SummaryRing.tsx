interface SummaryRingProps {
 value: number;
 max: number;
 size?: number;
 color: string;
}

export function SummaryRing(
 { value, max, size = 40, color }: SummaryRingProps,
) {
 const strokeWidth = 3;
 const radius = (size - strokeWidth) / 2;
 const circumference = 2 * Math.PI * radius;
 const pct = max > 0 ? Math.min(value / max, 1) : 0;
 const offset = circumference - pct * circumference;
 const center = size / 2;

 return (
 <div
 class="relative inline-flex items-center justify-center"
 style={{ width: `${size}px`, height: `${size}px` }}
 >
 <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`}>
 {/* Track */}
 <circle
 cx={center}
 cy={center}
 r={radius}
 fill="none"
 stroke="var(--bg-elevated)"
 stroke-width={strokeWidth}
 />
 {/* Progress */}
 <circle
 cx={center}
 cy={center}
 r={radius}
 fill="none"
 stroke={color}
 stroke-width={strokeWidth}
 stroke-linecap="round"
 stroke-dasharray={circumference}
 stroke-dashoffset={offset}
 transform={`rotate(-90 ${center} ${center})`}
 style={{ transition:"stroke-dashoffset 0.5s ease" }}
 />
 </svg>
 <div class="absolute inset-0 flex items-center justify-center">
 <span
 class="text-xs font-semibold"
 style={{ color:"var(--text-primary)" }}
 >
 {value}
 </span>
 </div>
 </div>
 );
}
