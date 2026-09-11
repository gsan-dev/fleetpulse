/** @type {import('next').NextConfig} */
const nextConfig = {
  // standalone empaqueta node_modules mínimos junto al build: es lo que
  // consume web/Dockerfile para no arrastrar el árbol de dependencias entero
  // a la imagen final.
  output: "standalone",
  eslint: {
    // El lint del monorepo Go no aplica aquí; se deja fuera del build para
    // no bloquear `npm run build` por avisos de estilo.
    ignoreDuringBuilds: true,
  },
};

export default nextConfig;
