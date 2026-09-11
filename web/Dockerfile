# Imagen del dashboard (Next.js, output standalone). Build multi-stage para
# no arrastrar node_modules de desarrollo a la imagen final.
#
#   docker build -f web/Dockerfile -t fleetpulse-web --build-arg NEXT_PUBLIC_API_URL=https://api.tu-dominio.com web

FROM node:22-alpine AS deps
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm ci

FROM node:22-alpine AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .

# NEXT_PUBLIC_* se embebe en el bundle en tiempo de build, no de arranque:
# hay que reconstruir la imagen para apuntar a otro servidor.
ARG NEXT_PUBLIC_API_URL
ENV NEXT_PUBLIC_API_URL=${NEXT_PUBLIC_API_URL}
RUN npm run build

FROM node:22-alpine AS run
WORKDIR /app
ENV NODE_ENV=production
RUN addgroup -S fleetpulse && adduser -S fleetpulse -G fleetpulse

COPY --from=build /app/.next/standalone ./
COPY --from=build /app/.next/static ./.next/static
COPY --from=build /app/public ./public

USER fleetpulse
EXPOSE 3000
CMD ["node", "server.js"]
