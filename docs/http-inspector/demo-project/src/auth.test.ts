import { strict as assert } from "node:assert";
import { test } from "node:test";
import { login } from "./auth.ts";

test("login aceita a senha correta", () => {
  assert.equal(login("aluno@example.com", "senha-de-exemplo"), true);
});

test("login recusa usuário desconhecido", () => {
  assert.equal(login("outro@example.com", "x"), false);
});
