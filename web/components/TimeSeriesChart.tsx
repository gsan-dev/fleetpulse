"use client";

import { Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis, CartesianGrid, Legend } from "recharts";
import type { MetricPoint } from "@/lib/types";
import { formatClockTime } from "@/lib/format";

export interface SeriesSpec {
  key: keyof MetricPoint;
  label: string;
  color: string;
}

/**
 * TimeSeriesChart es la unica grafica de lineas del dashboard, parametrizada
 * por serie: un eje (nunca dos escalas), una gridline recesiva, marcas finas
 * de 2px y tooltip con crosshair (todo lo trae Recharts por defecto), y
 * leyenda solo cuando hay mas de una serie — con una sola, el titulo de la
 * tarjeta que envuelve la grafica ya la nombra.
 */
export function TimeSeriesChart({
  data,
  series,
  valueFormatter,
}: {
  data: MetricPoint[];
  series: SeriesSpec[];
  valueFormatter: (value: number) => string;
}) {
  if (data.length === 0) {
    return <div className="flex h-48 items-center justify-center text-sm text-ink-muted">Esperando metricas…</div>;
  }

  return (
    <ResponsiveContainer width="100%" height={192}>
      <LineChart data={data} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}>
        <CartesianGrid stroke="var(--gridline)" vertical={false} />
        <XAxis
          dataKey="timestamp"
          tickFormatter={(value: string) => formatClockTime(value)}
          stroke="var(--baseline)"
          tick={{ fill: "var(--text-muted)", fontSize: 11 }}
          tickLine={false}
          axisLine={{ stroke: "var(--baseline)" }}
          minTickGap={40}
        />
        <YAxis
          stroke="var(--baseline)"
          tick={{ fill: "var(--text-muted)", fontSize: 11 }}
          tickLine={false}
          axisLine={false}
          width={48}
          tickFormatter={(value: number) => valueFormatter(value)}
        />
        <Tooltip
          contentStyle={{
            background: "var(--surface-1)",
            border: "1px solid var(--gridline)",
            borderRadius: 6,
            fontSize: 12,
          }}
          labelFormatter={(value: string) => formatClockTime(value)}
          formatter={(value: number, name: string) => [valueFormatter(value), name]}
        />
        {series.length > 1 && <Legend wrapperStyle={{ fontSize: 12 }} />}
        {series.map((s) => (
          <Line
            key={s.key}
            type="monotone"
            dataKey={s.key}
            name={s.label}
            stroke={s.color}
            strokeWidth={2}
            dot={false}
            activeDot={{ r: 4 }}
            isAnimationActive={false}
          />
        ))}
      </LineChart>
    </ResponsiveContainer>
  );
}
