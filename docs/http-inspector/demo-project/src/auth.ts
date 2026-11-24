// Projeto fictício da demonstração do HTTP Inspector (docs/http-inspector/TEACHING_DEMO.md).
// Contém, de propósito, uma comparação de senha ingênua para o modelo comentar.

export interface User {
  email: string;
  password: string;
}

const users: User[] = [{ email: "aluno@example.com", password: "senha-de-exemplo" }];

export function login(email: string, password: string): boolean {
  const user = users.find((u) => u.email === email);
  if (!user) {
    return false;
  }
  return user.password === password;
}
