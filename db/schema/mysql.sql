/*M!999999\- enable the sandbox mode */
-- MariaDB dump 10.19-11.4.8-MariaDB, for osx14.0 (arm64)
--
-- Host: 127.0.0.1    Database: migration-db
-- ------------------------------------------------------
-- Server version	11.4.8-MariaDB

/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;
/*!40101 SET @OLD_CHARACTER_SET_RESULTS=@@CHARACTER_SET_RESULTS */;
/*!40101 SET @OLD_COLLATION_CONNECTION=@@COLLATION_CONNECTION */;
/*!40101 SET NAMES utf8mb4 */;
/*!40103 SET @OLD_TIME_ZONE=@@TIME_ZONE */;
/*!40103 SET TIME_ZONE='+00:00' */;
/*!40014 SET @OLD_UNIQUE_CHECKS=@@UNIQUE_CHECKS, UNIQUE_CHECKS=0 */;
/*!40014 SET @OLD_FOREIGN_KEY_CHECKS=@@FOREIGN_KEY_CHECKS, FOREIGN_KEY_CHECKS=0 */;
/*!40101 SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_AUTO_VALUE_ON_ZERO' */;
/*M!100616 SET @OLD_NOTE_VERBOSITY=@@NOTE_VERBOSITY, NOTE_VERBOSITY=0 */;

--
-- Table structure for table `chunks`
--

/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!40101 SET character_set_client = utf8mb4 */;
CREATE TABLE `chunks` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `hash` varchar(64) NOT NULL,
  `size` int(10) unsigned NOT NULL,
  `created_at` timestamp NOT NULL DEFAULT current_timestamp(),
  `updated_at` timestamp NULL DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `hash` (`hash`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `config`
--

/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!40101 SET character_set_client = utf8mb4 */;
CREATE TABLE `config` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `key` varchar(255) NOT NULL,
  `value` text NOT NULL,
  `created_at` timestamp NOT NULL DEFAULT current_timestamp(),
  `updated_at` timestamp NULL DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_config_key` (`key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `nar_file_chunks`
--

/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!40101 SET character_set_client = utf8mb4 */;
CREATE TABLE `nar_file_chunks` (
  `nar_file_id` bigint(20) NOT NULL,
  `chunk_id` bigint(20) NOT NULL,
  `chunk_index` int(11) NOT NULL,
  PRIMARY KEY (`nar_file_id`,`chunk_index`),
  KEY `idx_nar_file_chunks_chunk_id` (`chunk_id`),
  CONSTRAINT `nar_file_chunks_ibfk_1` FOREIGN KEY (`nar_file_id`) REFERENCES `nar_files` (`id`) ON DELETE CASCADE,
  CONSTRAINT `nar_file_chunks_ibfk_2` FOREIGN KEY (`chunk_id`) REFERENCES `chunks` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `nar_files`
--

/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!40101 SET character_set_client = utf8mb4 */;
CREATE TABLE `nar_files` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `hash` varchar(255) NOT NULL,
  `compression` varchar(50) NOT NULL,
  `file_size` bigint(20) unsigned NOT NULL,
  `query` varchar(512) NOT NULL DEFAULT '',
  `created_at` timestamp NOT NULL DEFAULT current_timestamp(),
  `updated_at` timestamp NULL DEFAULT NULL,
  `last_accessed_at` timestamp NULL DEFAULT current_timestamp(),
  `total_chunks` int(11) NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_nar_files_hash_compression_query` (`hash`,`compression`,`query`) USING HASH,
  KEY `idx_nar_files_last_accessed_at` (`last_accessed_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `narinfo_nar_files`
--

/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!40101 SET character_set_client = utf8mb4 */;
CREATE TABLE `narinfo_nar_files` (
  `narinfo_id` bigint(20) NOT NULL,
  `nar_file_id` bigint(20) NOT NULL,
  PRIMARY KEY (`narinfo_id`,`nar_file_id`),
  KEY `idx_narinfo_nar_files_nar_file_id` (`nar_file_id`),
  CONSTRAINT `fk_narinfo_nar_files_nar_file` FOREIGN KEY (`nar_file_id`) REFERENCES `nar_files` (`id`) ON DELETE CASCADE,
  CONSTRAINT `fk_narinfo_nar_files_narinfo` FOREIGN KEY (`narinfo_id`) REFERENCES `narinfos` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `narinfo_references`
--

/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!40101 SET character_set_client = utf8mb4 */;
CREATE TABLE `narinfo_references` (
  `narinfo_id` bigint(20) NOT NULL,
  `reference` varchar(255) NOT NULL,
  PRIMARY KEY (`narinfo_id`,`reference`),
  KEY `idx_narinfo_references_reference` (`reference`),
  CONSTRAINT `fk_narinfo_references_narinfo_id` FOREIGN KEY (`narinfo_id`) REFERENCES `narinfos` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `narinfo_signatures`
--

/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!40101 SET character_set_client = utf8mb4 */;
CREATE TABLE `narinfo_signatures` (
  `narinfo_id` bigint(20) NOT NULL,
  `signature` varchar(255) NOT NULL,
  PRIMARY KEY (`narinfo_id`,`signature`),
  KEY `idx_narinfo_signatures_signature` (`signature`),
  CONSTRAINT `fk_narinfo_signatures_narinfo_id` FOREIGN KEY (`narinfo_id`) REFERENCES `narinfos` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `narinfos`
--

/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!40101 SET character_set_client = utf8mb4 */;
CREATE TABLE `narinfos` (
  `id` bigint(20) NOT NULL AUTO_INCREMENT,
  `hash` varchar(255) NOT NULL,
  `created_at` timestamp NOT NULL DEFAULT current_timestamp(),
  `updated_at` timestamp NULL DEFAULT NULL,
  `last_accessed_at` timestamp NULL DEFAULT current_timestamp(),
  `store_path` text DEFAULT NULL,
  `url` text DEFAULT NULL,
  `compression` varchar(255) DEFAULT NULL,
  `file_hash` varchar(255) DEFAULT NULL,
  `file_size` bigint(20) unsigned DEFAULT NULL,
  `nar_hash` varchar(255) DEFAULT NULL,
  `nar_size` bigint(20) unsigned DEFAULT NULL,
  `deriver` text DEFAULT NULL,
  `system` varchar(255) DEFAULT NULL,
  `ca` text DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_narinfos_hash` (`hash`),
  KEY `idx_narinfos_last_accessed_at` (`last_accessed_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Table structure for table `schema_migrations`
--

/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!40101 SET character_set_client = utf8mb4 */;
CREATE TABLE `schema_migrations` (
  `version` varchar(128) NOT NULL,
  PRIMARY KEY (`version`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Dumping routines for database 'migration-db'
--
/*!40103 SET TIME_ZONE=@OLD_TIME_ZONE */;

/*!40101 SET SQL_MODE=@OLD_SQL_MODE */;
/*!40014 SET FOREIGN_KEY_CHECKS=@OLD_FOREIGN_KEY_CHECKS */;
/*!40014 SET UNIQUE_CHECKS=@OLD_UNIQUE_CHECKS */;
/*!40101 SET CHARACTER_SET_CLIENT=@OLD_CHARACTER_SET_CLIENT */;
/*!40101 SET CHARACTER_SET_RESULTS=@OLD_CHARACTER_SET_RESULTS */;
/*!40101 SET COLLATION_CONNECTION=@OLD_COLLATION_CONNECTION */;
/*M!100616 SET NOTE_VERBOSITY=@OLD_NOTE_VERBOSITY */;

-- Dump completed

--
-- Dbmate schema migrations
--

LOCK TABLES `schema_migrations` WRITE;
INSERT INTO `schema_migrations` (version) VALUES
  ('20260101000000'),
  ('20260117195000'),
  ('20260127223000'),
  ('20260131021850'),
  ('20260205063659');
UNLOCK TABLES;
