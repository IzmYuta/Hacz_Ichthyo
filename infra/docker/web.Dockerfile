FROM node:20 as build
WORKDIR /app
COPY apps/web ./apps/web
WORKDIR /app/apps/web
ENV CI=true
RUN npm i -g pnpm && pnpm i && pnpm build

FROM node:20-slim
RUN apt-get update && apt-get install -y curl && rm -rf /var/lib/apt/lists/*
ENV PORT=8080
EXPOSE 8080
WORKDIR /app
COPY --from=build /app/apps/web/.next ./.next
COPY --from=build /app/apps/web/package.json ./package.json
COPY --from=build /app/apps/web/next.config.ts ./next.config.ts
COPY --from=build /app/apps/web/public ./public
COPY --from=build /app/apps/web/node_modules ./node_modules
CMD ["npx", "next", "start", "-p", "8080"]
