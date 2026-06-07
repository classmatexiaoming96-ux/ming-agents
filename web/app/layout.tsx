import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "SHRIMP Console",
  description: "Task queue status for the SHRIMP daemon",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
