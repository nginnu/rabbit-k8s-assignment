-- =============================================================================
-- MariaDB init — schema and seed
--
-- Run by a Job rather than the container entrypoint, so it must be safe to run
-- more than once: every statement is IF NOT EXISTS or INSERT IGNORE.
-- Re-applying the manifests re-runs the Job and must not fail.
--
-- Target: MariaDB 11, InnoDB, utf8mb4
-- =============================================================================

CREATE DATABASE IF NOT EXISTS rabbitshop
  CHARACTER SET utf8mb4
  COLLATE utf8mb4_unicode_ci;

USE rabbitshop;

-- =============================================================================
-- DDL — Tables
-- =============================================================================

-- -----------------------------------------------------------------------------
-- users
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `users` (
  `id`            INT           NOT NULL AUTO_INCREMENT,
  `username`      VARCHAR(64)   NOT NULL,
  `password_hash` VARCHAR(255)  NOT NULL COMMENT 'bcrypt',
  `created_at`    TIMESTAMP     NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_users_username` (`username`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- products (Premier League jerseys)
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `products` (
  `id`         VARCHAR(32)    NOT NULL COMMENT 'e.g. LIV-H-24',
  `name`       VARCHAR(128)   NOT NULL,
  `price`      DECIMAL(10,2)  NOT NULL,
  `created_at` TIMESTAMP      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- orders (customer orders for products)
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `orders` (
  `id`         INT                                NOT NULL AUTO_INCREMENT,
  `user_id`    INT                                NOT NULL,
  `product_id` VARCHAR(32)                        NOT NULL,
  `status`     ENUM('pending','paid','cancelled') NOT NULL DEFAULT 'pending',
  `created_at` TIMESTAMP                          NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` TIMESTAMP                          NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  INDEX `idx_orders_user_status` (`user_id`, `status`),
  INDEX `idx_orders_product`     (`product_id`),
  CONSTRAINT `fk_orders_user`    FOREIGN KEY (`user_id`)    REFERENCES `users` (`id`),
  CONSTRAINT `fk_orders_product` FOREIGN KEY (`product_id`) REFERENCES `products` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- -----------------------------------------------------------------------------
-- payments
-- -----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS `payments` (
  `id`          INT                             NOT NULL AUTO_INCREMENT,
  `order_id`    INT                             NOT NULL,
  `amount`      DECIMAL(10,2)                   NOT NULL,
  `status`      ENUM('pending','paid','failed') NOT NULL DEFAULT 'pending',
  `gateway_ref` VARCHAR(64)                     DEFAULT NULL COMMENT 'reference from mock-payment',
  `created_at`  TIMESTAMP                       NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at`  TIMESTAMP                       NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  INDEX `idx_payments_order`  (`order_id`),
  INDEX `idx_payments_status` (`status`),
  CONSTRAINT `fk_payments_order` FOREIGN KEY (`order_id`) REFERENCES `orders` (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- =============================================================================
-- Seed Data
-- =============================================================================

-- -----------------------------------------------------------------------------
-- users (password = 'password', bcrypt cost 10 — lab use only)
-- ⚠ Hash ด้านล่างถูก verify ด้วย services/cmd/bcrypt-tool — ห้ามเปลี่ยนโดยไม่ verify
--   regen:  cd services && go run ./cmd/bcrypt-tool gen password
--   verify: cd services && go run ./cmd/bcrypt-tool verify password '<hash>'
-- -----------------------------------------------------------------------------
INSERT IGNORE INTO `users` (`username`, `password_hash`) VALUES
  ('alice', '$2a$10$6hJG3tfyDT2DExeZLYMo6OF5kJXoJX7SJin1ZkWmygrYejx7iO/DO'),
  ('bob',   '$2a$10$6hJG3tfyDT2DExeZLYMo6OF5kJXoJX7SJin1ZkWmygrYejx7iO/DO'),
  ('hana',  '$2a$10$6hJG3tfyDT2DExeZLYMo6OF5kJXoJX7SJin1ZkWmygrYejx7iO/DO'),
  ('felix', '$2a$10$6hJG3tfyDT2DExeZLYMo6OF5kJXoJX7SJin1ZkWmygrYejx7iO/DO'),
  ('sara',  '$2a$10$6hJG3tfyDT2DExeZLYMo6OF5kJXoJX7SJin1ZkWmygrYejx7iO/DO'),
  ('lily',  '$2a$10$6hJG3tfyDT2DExeZLYMo6OF5kJXoJX7SJin1ZkWmygrYejx7iO/DO');

-- -----------------------------------------------------------------------------
-- products — Premier League jerseys 2024/25
-- id pattern: <3-letter team code>-H-24 (Home) / -A-24 (Away)
-- ราคาใช้ THB
-- -----------------------------------------------------------------------------
INSERT IGNORE INTO `products` (`id`, `name`, `price`) VALUES
  ('LIV-H-24', 'Liverpool Home 2024/25',          3290.00),
  ('MCI-H-24', 'Manchester City Home 2024/25',    3490.00),
  ('ARS-H-24', 'Arsenal Home 2024/25',            3290.00),
  ('MUN-H-24', 'Manchester United Home 2024/25',  3290.00),
  ('CHE-H-24', 'Chelsea Home 2024/25',            3290.00),
  ('TOT-H-24', 'Tottenham Home 2024/25',          2990.00);

-- No orders or payments seeded — those are created by user flows.
