-- Demo accounts, one per wallet user (assignment 5.2, 8.1).
-- Passwords are documented in README.md; hashes made with cmd/hashpassword.
INSERT INTO accounts (user_id, username, password_hash, currency)
VALUES (34633089486, 'player1', '$2a$10$xpdVOL70p9fFpW4IgjEiVepGaVofBN.GRJpsKtoBZzzFcwe.7YJpu', 'USD'),
       (34679664254, 'player2', '$2a$10$nasWxwqHrHjLVUJynQWbXe1eT6cPktUTxeZKXKWKHVhgYWPevnDaG', 'EUR'),
       (34616761765, 'player3', '$2a$10$18YHxzkyBRudiJGrfJh44eg/4wyZV9dtlgPMdBjDmXtC9OMwedXZW', 'KES'),
       (34673635133, 'player4', '$2a$10$WMnUBXWm7VgB3wJ3ZCdfZ.Fne.iZfN6MuCy52LzzkmLyfwCmYIDUe', 'USD');
