import type { Metadata } from "next";
import Link from "next/link";
import "./globals.css";

export const metadata: Metadata = {
  title: "FleetPulse",
  description: "Monitorización de flotas de servidores y contenedores",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="es">
      <body className="min-h-screen font-sans">
        <header className="border-b border-line-border bg-surface">
          <div className="mx-auto flex max-w-6xl items-center gap-2 px-4 py-3">
            <Link href="/" className="flex items-center gap-2 font-semibold text-ink-primary">
              <span aria-hidden="true">📡</span> FleetPulse
            </Link>
          </div>
        </header>
        <main className="mx-auto max-w-6xl px-4 py-6">{children}</main>
      </body>
    </html>
  );
}
