# syntax=docker/dockerfile:1

FROM node:24-bookworm-slim

WORKDIR /workspace/web

COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web ./

EXPOSE 5173

CMD ["npm", "run", "dev", "--", "--host", "0.0.0.0"]
