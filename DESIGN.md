# Design

Work in progress. To be filled in:

1. Behaviour in each failure situation
2. How a PENDING transaction is resolved
3. Retry after a FAILED attempt
4. Zero-amount deposit
5. Other assumptions and what I would improve
- EN: The seed file holds precomputed bcrypt hashes. With more time I would hash the passwords at seed time instead (for example, the service creates the demo accounts on first start), so no hashes are kept in the repository.
- UA: Seed-файл містить заздалегідь пораховані bcrypt-хеші. Якби був час, я б хешувала паролі під час seed (наприклад, сервіс створює демо-акаунти при першому старті), щоб у репозиторії не було хешів.
