-- 秒杀商城数据库初始化脚本
-- 创建数据库
CREATE DATABASE IF NOT EXISTS seckill DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;

USE seckill;

-- 商品表
CREATE TABLE IF NOT EXISTS `product` (
    `id` BIGINT NOT NULL AUTO_INCREMENT COMMENT '商品ID',
    `name` VARCHAR(255) NOT NULL COMMENT '商品名称',
    `price` DECIMAL(10,2) NOT NULL COMMENT '商品价格',
    `stock` INT NOT NULL DEFAULT 0 COMMENT '库存数量',
    `description` VARCHAR(255) DEFAULT '' COMMENT '商品描述',
    PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='商品表';

-- 订单表
CREATE TABLE IF NOT EXISTS `orders` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '订单自增ID',
    `order_id` VARCHAR(64) NOT NULL COMMENT '订单号',
    `user_id` BIGINT NOT NULL COMMENT '用户ID',
    `product_id` BIGINT NOT NULL COMMENT '商品ID',
    `amount` DECIMAL(10,2) NOT NULL COMMENT '订单金额',
    `status` INT NOT NULL DEFAULT 0 COMMENT '订单状态: 0-待支付 1-已支付 2-已取消',
    `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_order_id` (`order_id`),
    KEY `idx_user_id` (`user_id`),
    KEY `idx_product_id` (`product_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='订单表';

-- 插入测试商品数据
INSERT INTO `product` (`id`, `name`, `price`, `stock`, `description`) VALUES
(1, 'iPhone 15 Pro', 7999.00, 100, '苹果最新旗舰手机'),
(2, 'MacBook Pro 14', 14999.00, 50, '专业级笔记本电脑'),
(3, 'AirPods Pro 2', 1899.00, 200, '主动降噪无线耳机');
