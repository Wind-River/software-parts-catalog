//go:build tkdb

package tkdb

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"

	"wrs/tkdb/goose/packages/archive/processor"
	"wrs/tkdb/goose/packages/archive/sync"
	"wrs/tkdb/goose/packages/archive/tree"
	"wrs/tkdb/goose/packages/file_collection"
	"wrs/tkdb/goose/packages/part"

	"strings"

	generic "wrs/tkdb/goose/packages/generics/graph"

	"github.com/pkg/errors"
	"github.com/pressly/goose/v3"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gitlab.devstar.cloud/ip-systems/verification-code.git/code"
	"gitlab.devstar.cloud/ip-systems/verification-code.git/code/legacy"
)

func init() {
	goose.AddMigration(upComplexCopy, downComplexCopy)
}

func upComplexCopy(tx *sql.Tx) error {
	// This code is executed when the migration is applied.

	// oldFileCollections, err := collectSpecificCollectionsToTest(tx)
	oldFileCollections, err := collectCollectionsWithLicenseData(tx)
	if err != nil {
		return err
	}

	archiveProcessor, err := processor.NewArchiveProcessor(tx, nil, nil)
	if err != nil {
		return err
	}

	for _, v := range oldFileCollections {
		if err := processCollectionsArchives(archiveProcessor, v); err == processor.ErrSha256 {
			log.Warn().Int64("old file_collection_id", v.FileCollectionID).Msg("Skipping File Collection missing sha256 files")
		} else if err != nil {
			return err
		}

		archiveProcessor.Reset()
	}

	return nil
}

func downComplexCopy(tx *sql.Tx) error {
	// This code is executed when the migration is rolled back.
	return nil
}

func collectSpecificCollectionsToTest(tx *sql.Tx) ([]OldFilecollection, error) {
	oldCollections := make([]OldFilecollection, 0)

	rows, err := tx.Query(`SELECT fc.id, fc.insert_date, fc.flag_extract, fc.flag_license_extracted, fc.license_rationale, fc.verification_code_one, fc.verification_code_two,
		le.expression
		FROM file_collection fc
		INNER JOIN license_expression le ON le.id=fc.license_id
		WHERE fc.license_id IS NOT NULL
		AND fc.id IN (269056)`)
	// AND (SELECT COUNT(*) FROM archive_table WHERE file_collection_id=fc.id) > 0`)
	// AND fc.id NOT IN (6081, 6008)`) // TODO properly filter out collections with no archives
	if err != nil {
		return nil, errors.Wrapf(err, "error selecting file_collections")
	}
	defer rows.Close()

	for rows.Next() {
		var tmp OldFilecollection
		var licenseRationale sql.NullString
		var flagExtract, flagLicenseExtracted int
		if err := rows.Scan(&tmp.FileCollectionID, &tmp.InsertDate, &flagExtract,
			&flagLicenseExtracted, &licenseRationale,
			&tmp.FileVerificationCodeOne, &tmp.FileVerificationCodeTwo,
			&tmp.LicenseExpression); err != nil {
			return nil, errors.Wrapf(err, "error scanning file_collections")
		}

		if licenseRationale.Valid {
			tmp.LicenseRationale = licenseRationale.String
		}
		tmp.Extracted = flagExtract > 0
		tmp.LicenseExtracted = flagLicenseExtracted > 0

		oldCollections = append(oldCollections, tmp)
	}
	rows.Close()

	return oldCollections, nil
}

func collectCollectionsWithLicenseData(tx *sql.Tx) ([]OldFilecollection, error) {
	oldCollections := make([]OldFilecollection, 0)

	rows, err := tx.Query(`SELECT fc.id, fc.insert_date, fc.flag_extract, fc.flag_license_extracted, fc.license_rationale, fc.verification_code_one, fc.verification_code_two,
		le.expression
		FROM file_collection fc
		INNER JOIN license_expression le ON le.id=fc.license_id
		WHERE fc.license_id IS NOT NULL`)
	// AND (SELECT COUNT(*) FROM archive_table WHERE file_collection_id=fc.id) > 0`)
	// AND fc.id NOT IN (6081, 6008)`) // TODO properly filter out collections with no archives
	if err != nil {
		return nil, errors.Wrapf(err, "error selecting file_collections")
	}
	defer rows.Close()

	for rows.Next() {
		var tmp OldFilecollection
		var licenseRationale sql.NullString
		var flagExtract, flagLicenseExtracted int
		if err := rows.Scan(&tmp.FileCollectionID, &tmp.InsertDate, &flagExtract,
			&flagLicenseExtracted, &licenseRationale,
			&tmp.FileVerificationCodeOne, &tmp.FileVerificationCodeTwo,
			&tmp.LicenseExpression); err != nil {
			return nil, errors.Wrapf(err, "error scanning file_collections")
		}

		if licenseRationale.Valid {
			tmp.LicenseRationale = licenseRationale.String
		}
		tmp.Extracted = flagExtract > 0
		tmp.LicenseExtracted = flagLicenseExtracted > 0

		oldCollections = append(oldCollections, tmp)
	}
	rows.Close()

	return oldCollections, nil
}

func processCollectionsArchives(archiveProcessor *processor.ArchiveProcessor, ofc OldFilecollection) error {
	logger := log.With().Str(zerolog.CallerFieldName, "processCollectionsArchives").Int64("file_collection_id", ofc.FileCollectionID).Logger()
	logger.Debug().Msg("start")
	defer logger.Debug().Msg("end")

	// find archives
	archiveIDs := make([]int64, 0)
	rows, err := archiveProcessor.Tx.Query(`SELECT id FROM archive_table 
	WHERE file_collection_id=$1 AND checksum_sha1 IS NOT NULL AND extract_status<>-404`,
		ofc.FileCollectionID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var tmp int64
		if err := rows.Scan(&tmp); err != nil {
			return errors.Wrapf(err, "error scanning archive of collection %d", ofc.FileCollectionID)
		}

		archiveIDs = append(archiveIDs, tmp)
	}
	logger.Debug().Interface("archiveIDs", archiveIDs).Msg("collected archives")

	var root tree.Node
	if len(archiveIDs) == 0 {
		// log.Warn().Int64("file_collection_id", ofc.FileCollectionID).Msg("Skipping Collection With No Archives")
		logger.Debug().Msg("processing file_collcetion")
		root, err = archiveProcessor.ProcessCollection(ofc.FileCollectionID)
		if err != nil {
			return err
		}
	} else {
		logger.Debug().Msg("processing archive")
		// process one
		root, err = archiveProcessor.ProcessArchive(archiveIDs[0], nil)
		if err != nil {
			return errors.Wrapf(err, "error processing archive %d", archiveIDs[0])
		}
	}
	logger.Debug().Msg("processed root node")

	// calculate verification code
	if err := tree.CalculateVerificationCodes(root); err != nil {
		return err
	}
	logger.Debug().Hex("file_verification_code", root.GetFileVerificationCode()).Msg("calculated verification codes")

	rootUUID, err := sync.SyncTree(archiveProcessor.Tx, &part.PartController{DB: archiveProcessor.Tx}, root)
	if err != nil {
		return err
	}
	logger = logger.With().Str("uuid", rootUUID.String()).Logger()
	logger.Debug().Msg("synced tree")

	logger.Debug().Interface("archiveIDs", archiveIDs).Msg("upserting other archives")
	// upsert other archives
	for _, v := range archiveIDs {
		a, err := processor.InitArchive(archiveProcessor.Tx, v)
		if err != nil {
			return err
		}

		// upsert archive and archive_alias
		if _, err := archiveProcessor.Tx.Exec(`INSERT INTO archive (sha256, archive_size, md5, sha1, part_id) VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (sha256) DO UPDATE SET part_id=EXCLUDED.part_id`,
			a.Sha256[:], a.Size, a.Md5[:], a.Sha1[:], rootUUID); err != nil {
			return errors.Wrapf(err, "error upserting archive")
		}

		if _, err := archiveProcessor.Tx.Exec(`INSERT INTO archive_alias (archive_sha256, name) VALUES ($1, $2) ON CONFLICT (archive_sha256, name) DO NOTHING`,
			a.Sha256[:], a.GetName()); err != nil {
			return errors.Wrapf(err, "error upserting archive_alias")
		}
	}
	logger.Debug().Msg("upserted other archives")

	return nil
}

// Represents file_collection in old model
// file_collection -> part
type OldFilecollection struct {
	FileCollectionID        int64     `db:"id"`
	InsertDate              time.Time `db:"insert_date"`
	groupContainerID        int       `db:"group_container_id"` // TODO group name instead of id
	Extracted               bool      `db:"flag_extract"`
	LicenseExtracted        bool      `db:"flag_license_extracted"`
	LicenseID               int64     `db:"license_id"`
	LicenseRationale        string    `db:"license_rationale"`
	analystID               int64     `db:"analyst_id"`
	LicenseExpression       string    `db:"license_expression"`
	LicenseNotice           string    `db:"license_notice"`
	Copyright               string    `db:"copyright"`
	FileVerificationCodeOne []byte    `db:"verification_code_one"`
	FileVerificationCodeTwo []byte    `db:"verification_code_two"`
}

func updateFileCollectionVerificationCodes(conn *sql.Tx) error {
	fileCollectionController := TransactionFileCollectionController{Tx: conn}

	if err := updateCompleteCollections(conn, &fileCollectionController); err != nil {
		log.Fatal().Err(err).Str("expanded", fmt.Sprintf("%+v", err)).Msg("error updateCompleteCollections")
	}

	if err := updateCollectionsWithOneButNotTwo(conn, &fileCollectionController); err != nil {
		log.Fatal().Err(err).Str("expanded", fmt.Sprintf("%+v", err)).Msg("error updateCollectionsWithOneButNotTwo")
	} else {
		log.Info().Msg("finished updateCollectionsWithOneButNotTwo")
	}

	if err := updateCollectionsMissingVerificationCodes(conn, &fileCollectionController); err != nil {
		log.Fatal().Err(err).Str("expanded", fmt.Sprintf("%+v", err)).Msg("error updateCollectionsMissingVerificationCodes")
	} else {
		log.Info().Msg("finished updateCollectionsMissingVerificationCodes")
	}

	log.Info().Msg("Done")
	return nil
}

const (
	FVC1Key = "file_collection_verification_code_one_key"
	FVC2Key = "file_collection_verification_code_two_key"
)

func determineNewestFileCollection(conn *sql.Tx, fileCollectionController *TransactionFileCollectionController, fileCollectionID int64, fvcOne []byte, fvcTwo []byte, conflictingFileCollectionID int64) (new *file_collection.FileCollection, old *file_collection.FileCollection, err error) {
	// logger := log.With().Str(zerolog.CallerFieldName, "determineNewestFileCollection").Logger()
	other, err := fileCollectionController.GetByID(conflictingFileCollectionID)
	if err != nil {
		return nil, nil, err
	}

	fileCollection, err := fileCollectionController.GetByID(fileCollectionID)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "could not get fileCollection by ID")
	}

	// If only one has a rationale, pick that one
	switch {
	case fileCollection.LicenseRationale.Valid && !other.LicenseRationale.Valid:
		return fileCollection, other, nil
	case other.LicenseRationale.Valid && !fileCollection.LicenseRationale.Valid:
		return other, fileCollection, nil
	}

	// If both have rationales parse them
	if fileCollection.LicenseRationale.Valid && other.LicenseRationale.Valid {
		// Parse Rationale Date and see if it matches insert date
		var rationaleDateString, otherDateString string
		dateIdx := strings.Index(fileCollection.LicenseRationale.String, "Date:")
		if dateIdx != -1 {
			barIdx := strings.Index(fileCollection.LicenseRationale.String[dateIdx+5:], "|")
			if barIdx != -1 {
				// log.Debug().
				// 	Str("rationaleDateString", rationaleDateString).Int("dateIdx", dateIdx).Int("barIdx", barIdx).
				// 	Interface("fileCollection", fileCollection).
				// 	Send()
				rationaleDateString = strings.TrimSpace(fileCollection.LicenseRationale.String[dateIdx+5 : barIdx+dateIdx+5])
			}
		}
		dateIdx = strings.Index(other.LicenseRationale.String, "Date:")
		if dateIdx != -1 {
			barIdx := strings.Index(other.LicenseRationale.String[dateIdx+5:], "|")
			if barIdx != -1 {
				// log.Debug().
				// 	Str("otherDateString", otherDateString).Int("dateIdx", dateIdx).Int("barIdx", barIdx).
				// 	Interface("fileCollection", fileCollection).
				// 	Send()
				otherDateString = strings.TrimSpace(other.LicenseRationale.String[dateIdx+5 : barIdx+dateIdx+5])
			}
		}

		if rationaleDateString != "" && otherDateString != "" { // both have date strings
			// replace July with Jul
			rationaleDateString = strings.Replace(rationaleDateString, "July", "Jul", 1)
			otherDateString = strings.Replace(otherDateString, "July", "Jul", 1)
			// replace Romanian July(Iulie) with Jul
			rationaleDateString = strings.Replace(rationaleDateString, "Iulie", "Jul", 1)
			otherDateString = strings.Replace(otherDateString, "Iulie", "Jul", 1)
			// replace Romanian Jan(Ian) with Jan
			rationaleDateString = strings.Replace(rationaleDateString, "Ian", "Jan", 1)
			otherDateString = strings.Replace(otherDateString, "Ian", "Jan", 1)
			// replace Sept with Sep
			rationaleDateString = strings.Replace(rationaleDateString, "Sept", "Sep", 1)
			otherDateString = strings.Replace(otherDateString, "Sept", "Sep", 1)
			// replace June with Jun
			rationaleDateString = strings.Replace(rationaleDateString, "June", "Jun", 1)
			otherDateString = strings.Replace(otherDateString, "June", "Jun", 1)
			// replace March with Mar
			rationaleDateString = strings.Replace(rationaleDateString, "March", "Mar", 1)
			otherDateString = strings.Replace(otherDateString, "March", "Mar", 1)
			// fix truncated year; no guarantee this is the right year, just making a guess
			truncatedYearPattern := regexp.MustCompile(`\-202$`)
			rationaleDateString = truncatedYearPattern.ReplaceAllLiteralString(rationaleDateString, "-2020")
			otherDateString = truncatedYearPattern.ReplaceAllLiteralString(otherDateString, "-2020")

			var rationaleDate, otherDate time.Time
			if d, err := parseDateTime(rationaleDateString); err != nil {
				return nil, nil, errors.Wrapf(err, "error parsing rationale date \"%s\" of %d", rationaleDateString, fileCollection.FileCollectionID)
			} else {
				rationaleDate = *d
			}

			if d, err := parseDateTime(otherDateString); err != nil {
				return nil, nil, errors.Wrapf(err, "error parsing %s of %d", otherDateString, other.FileCollectionID)
			} else {
				rationaleDate = *d
			}

			switch {
			case rationaleDate.After(otherDate):
				return fileCollection, other, nil
			case rationaleDate.Before(otherDate):
				return other, fileCollection, nil
			case rationaleDate.Equal(otherDate):
				switch {
				case fileCollection.InsertDate.After(other.InsertDate):
					return fileCollection, other, nil
				case fileCollection.InsertDate.Before(other.InsertDate):
					return other, fileCollection, nil
				default:
					return nil, nil, errors.Wrapf(err, "Both collections are at the same rationale and insert date, so I should calculate archive count for %d and %d",
						fileCollection.FileCollectionID, other.FileCollectionID)
				}
			}
		}
	}

	// otherwise rely on insert date
	switch {
	case fileCollection.InsertDate.After(other.InsertDate):
		return fileCollection, other, nil
	case fileCollection.InsertDate.Before(other.InsertDate):
		return other, fileCollection, nil
	default:
		return nil, nil, errors.Wrapf(err, "Both collections are at the same date, so I should calculate archive count for %d and %d",
			fileCollection.FileCollectionID, other.FileCollectionID)
	}
}

func verifyCompleteCollections(conn *sql.Tx, fileCollectionController *TransactionFileCollectionController) error {
	logger := log.With().Str(zerolog.CallerFieldName, "verifyCompleteCollections").Logger()

	rows, err := conn.Query(`SELECT id 
	FROM file_collection 
	WHERE verification_code_two IS NOT NULL 
	AND verification_code_one IS NOT NULL 
	ORDER BY id`)
	if err != nil {
		return errors.Wrapf(err, "error selecting file_collections to work on")
	}
	defer rows.Close()

	collectionsToVerify := make([]int64, 0)
	for rows.Next() {
		var tmp int64
		if err := rows.Scan(&tmp); err != nil {
			return errors.Wrapf(err, "error scanning file_collection_id")
		}

		collectionsToVerify = append(collectionsToVerify, tmp)
	}
	rows.Close()

	logger.Info().Int("collectionsToVerify", len(collectionsToVerify)).Send()

	for _, fileCollectionID := range collectionsToVerify {
		// time.Sleep(time.Second)
		logger.Debug().Int64("fileCollectionID", fileCollectionID).Msg("Starting on File Collection")

		calculatedFvcOne, calculatedFvcTwo, err := fileCollectionController.CalculateFileCollectionVerificationCode(fileCollectionID)
		if err != nil {
			return errors.Wrapf(err, "error calculating file verification codes of %d", fileCollectionID)
		}

		var dataFvcOne, dataFvcTwo []byte
		if err := conn.QueryRow("SELECT verification_code_one, verification_code_two FROM file_collection WHERE id=$1",
			fileCollectionID).Scan(&dataFvcOne, &dataFvcTwo); err != nil {
			return errors.Wrapf(err, "error selecting verification codes")
		}

		if !bytes.Equal(calculatedFvcOne, dataFvcOne) || !bytes.Equal(calculatedFvcTwo, dataFvcTwo) {
			logger.Error().Hex("calculatedFvcOne", calculatedFvcOne).Hex("calculatedFvcTwo", calculatedFvcTwo).
				Hex("dataFvcOne", dataFvcOne).Hex("dataFvcTwo", dataFvcTwo).Msg("FVC mismatch")
			return errors.New("FVC mismatch")
		}
	}
	logger.Info().Msg("Returning")

	return nil
}

func updateCompleteCollections(conn *sql.Tx, fileCollectionController *TransactionFileCollectionController) error {
	logger := log.With().Str(zerolog.CallerFieldName, "updateCompleteCollections").Logger()

	rows, err := conn.Query("SELECT id FROM file_collection WHERE verification_code_two IS NOT NULL AND verification_code_one IS NOT NULL ORDER BY id")
	if err != nil {
		return errors.Wrapf(err, "error selecting file_collections to work on")
	}
	defer rows.Close()

	collectionsToUpdate := make([]int64, 0)
	for rows.Next() {
		var tmp int64
		if err := rows.Scan(&tmp); err != nil {
			return errors.Wrapf(err, "error scanning file_collction_id")
		}

		collectionsToUpdate = append(collectionsToUpdate, tmp)
	}
	rows.Close()

	logger.Info().Int("collectionsToUpdate", len(collectionsToUpdate)).Send()

	removedCollections := make(map[int64]bool)
	for _, fileCollectionID := range collectionsToUpdate {
		// time.Sleep(time.Second)
		if removedCollections[fileCollectionID] {
			logger.Warn().Int64("removed", fileCollectionID).Msg("Skipping Removed Collection")
			continue
		}
		logger.Debug().Int64("fileCollectionID", fileCollectionID).Msg("Starting on File Collection")

		fvcOne, fvcTwo, err := fileCollectionController.CalculateFileCollectionVerificationCode(fileCollectionID)
		if err != nil {
			return errors.Wrapf(err, "error calculating file verification codes of %d", fileCollectionID)
		}

		if removed, err := updateMayConflict(conn, fileCollectionController, fileCollectionID, fvcOne, fvcTwo); err != nil {
			return errors.Wrapf(err, "error updating file verification code of %d to (%x, %x)", fileCollectionID, fvcOne, fvcTwo)
		} else if removed > 0 {
			removedCollections[removed] = true
		}
	}
	logger.Info().Msg("Returning")

	return nil
}

func updateCollectionsWithOneButNotTwo(conn *sql.Tx, fileCollectionController *TransactionFileCollectionController) error {
	logger := log.With().Str(zerolog.CallerFieldName, "updateCollectionsWithOneButNotTwo").Logger()

	rows, err := conn.Query(`SELECT id 
	FROM file_collection 
	WHERE verification_code_two IS NULL 
	AND verification_code_one IS NOT NULL 
	ORDER BY id`)
	if err != nil {
		return errors.Wrapf(err, "error selecting file_collections to work on")
	}
	defer rows.Close()

	collectionsToUpdate := make([]int64, 0)
	for rows.Next() {
		var tmp int64
		if err := rows.Scan(&tmp); err != nil {
			return errors.Wrapf(err, "error scanning file_collction_id")
		}

		collectionsToUpdate = append(collectionsToUpdate, tmp)
	}
	rows.Close()

	logger.Info().Int("collectionsToUpdate", len(collectionsToUpdate)).Send()

	removedCollections := make(map[int64]bool)
	for _, fileCollectionID := range collectionsToUpdate {
		// time.Sleep(time.Second)
		if removedCollections[fileCollectionID] {
			logger.Warn().Int64("removed", fileCollectionID).Msg("Skipping Removed Collection")
			continue
		}
		logger.Debug().Int64("fileCollectionID", fileCollectionID).Msg("Starting on File Collection")

		fvcOne, fvcTwo, err := fileCollectionController.CalculateFileCollectionVerificationCode(fileCollectionID)
		if err != nil {
			return errors.Wrapf(err, "error calculating file verification codes of %d", fileCollectionID)
		}

		if removed, err := updateMayConflict(conn, fileCollectionController, fileCollectionID, fvcOne, fvcTwo); err != nil {
			return errors.Wrapf(err, "error updating file verification code of %d to (%x, %x)", fileCollectionID, fvcOne, fvcTwo)
		} else if removed > 0 {
			removedCollections[removed] = true
		}
	}
	logger.Info().Msg("Returning")

	return nil
}

func updateCollectionsMissingVerificationCodes(conn *sql.Tx, fileCollectionController *TransactionFileCollectionController) error {
	logger := log.With().Str(zerolog.CallerFieldName, "updateCollectionsMissingVerificationCodes").Logger()

	rows, err := conn.Query("SELECT id FROM file_collection WHERE verification_code_two IS NULL AND verification_code_one IS NULL ORDER BY id")
	if err != nil {
		return errors.Wrapf(err, "error selecting file_collections to work on")
	}
	defer rows.Close()

	collectionsToUpdate := make([]int64, 0)
	for rows.Next() {
		var tmp int64
		if err := rows.Scan(&tmp); err != nil {
			return errors.Wrapf(err, "error scanning file_collction_id")
		}

		collectionsToUpdate = append(collectionsToUpdate, tmp)
	}
	rows.Close()

	logger.Info().Int("collectionsToUpdate", len(collectionsToUpdate)).Send()

	for _, fileCollectionID := range collectionsToUpdate {
		time.Sleep(time.Second)
		logger.Debug().Int64("fileCollectionID", fileCollectionID).Msg("Starting on File Collection")

		fvcOne, fvcTwo, err := fileCollectionController.CalculateFileCollectionVerificationCode(fileCollectionID)
		if err != nil {
			return errors.Wrapf(err, "error calculating file verification codes of %d", fileCollectionID)
		}
		logger.Debug().Int64("fileCollectionID", fileCollectionID).Hex("fvcOne", fvcOne).Hex("fvcTwo", fvcTwo).Msg("Calculated Verification Codes")

		if _, err := updateMayConflict(conn, fileCollectionController, fileCollectionID, fvcOne, fvcTwo); err != nil {
			return errors.Wrapf(err, "error updating file_collection of %d to (%x, %x)", fileCollectionID, fvcOne, fvcTwo)
		}
	}

	return nil
}

func updateMayConflict(tx *sql.Tx, fileCollectionController *TransactionFileCollectionController, fileCollectionID int64, fvcOne []byte, fvcTwo []byte) (removed int64, err error) {
	logger := log.With().Str(zerolog.CallerFieldName, "updateMayConflict").Int64("fileCollectionID", fileCollectionID).Hex("fvcOne", fvcOne).Hex("fvcTwo", fvcTwo).Logger()
	logger.Debug().Send()

	preparedUpdate, err := tx.Prepare("UPDATE file_collection SET verification_code_one=$2, verification_code_two=$3 WHERE id=$1")
	if err != nil {
		return 0, errors.Wrapf(err, "error preparing update statement")
	}

	// determine whether there will be a conflict
	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM file_collection WHERE id<>$1 AND (verification_code_one=$2 OR verification_code_two=$3)",
		fileCollectionID, fvcOne, fvcTwo).Scan(&count); err != nil {
		return 0, errors.Wrapf(err, "error checking file_collection %d (%x, %x) for conflicts", fileCollectionID, fvcOne, fvcTwo)
	}

	if count == 0 { // just update, there should be no conflict
		if _, err := preparedUpdate.Exec(fileCollectionID, fvcOne, fvcTwo); err != nil {
			return 0, errors.Wrapf(err, "unexpected error while updating file_collection %d with (%x, %x)",
				fileCollectionID, fvcOne, fvcTwo)
		}

		return 0, nil
	} else if count > 1 { // more conflicts than we are prepared to deal with
		logger.Warn().Int("count", count).Msg("More conflicts than expected")
		return 0, errors.New(fmt.Sprintf("More confilcts than expected: %d", count))
	}

	var conflictingFileCollectionID int64
	if err := tx.QueryRow("SELECT id FROM file_collection WHERE verification_code_one=$1 OR verification_code_two=$2",
		fvcOne, fvcTwo).Scan(&conflictingFileCollectionID); err != nil {
		return 0, errors.Wrapf(err, "error looking for conflict of file_collection %d (%x, %x)", fileCollectionID, fvcOne, fvcTwo)
	}

	// just one conlfict to resolve
	newer, older, err := determineNewestFileCollection(tx, fileCollectionController, fileCollectionID, fvcOne, fvcTwo, conflictingFileCollectionID)
	if err != nil {
		return 0, err
	} else if newer == nil || older == nil {
		return 0, errors.New(fmt.Sprintf("newer: %#v\nolder: %#v\nfileCollectionID: %d\nfvcOne: %x\nfvcTwo: %x\n",
			newer,
			older,
			fileCollectionID,
			fvcOne,
			fvcTwo,
		))
	}

	// check if older owned by newer
	var isParent bool
	if err := tx.QueryRow("SELECT EXISTS (SELECT 1 FROM file_collection_contains WHERE parent_id=$1 AND child_id=$2)",
		newer.FileCollectionID, older.FileCollectionID).Scan(&isParent); err != nil {
		return 0, errors.Wrapf(err, "error checking if parent/child")
	}

	if isParent {
		// check if newer has no other files or collections
		var subCollectionCount int
		if err := tx.QueryRow("SELECT COUNT(*) FROM file_collection_contains WHERE parent_id=$1 AND child_id<>$2",
			newer.FileCollectionID, older.FileCollectionID).Scan(&subCollectionCount); err != nil {
			return 0, errors.Wrapf(err, "error counting children")
		}

		if subCollectionCount == 0 {
			var fileCount int
			if err := tx.QueryRow("SELECT COUNT(*) FROM file_belongs_collection WHERE file_collection_id=$1",
				newer.FileCollectionID).Scan(&fileCount); err != nil {
				return 0, errors.Wrapf(err, "error counting files")
			}

			if fileCount == 0 { // is solely a sub archive
				return resolveSubArchive(tx, fileCollectionController, newer, older, fvcOne, fvcTwo)
			}
		}
	}

	logger.Debug().Msg("Resloving generic conflict")

	// move archives from old to new
	log.Debug().Int64("older.FileCollectionID", older.FileCollectionID).Int64("newer.FileCollectionID", newer.FileCollectionID).Msg("Moving archive_tables")
	if _, err := tx.Exec("UPDATE archive_table SET file_collection_id=$1 WHERE file_collection_id=$2",
		newer.FileCollectionID, older.FileCollectionID); err != nil {
		return 0, errors.Wrapf(err, "error moving archives from %d to %d", newer.FileCollectionID, older.FileCollectionID)
	}

	// delete owned file_collection_contains
	if _, err := tx.Exec("DELETE FROM file_collection_contains WHERE parent_id=$1",
		older.FileCollectionID); err != nil {
		return 0, errors.Wrapf(err, "error deleting owned file_collections")
	}

	// move file_collection_contains from old to new
	sql := "UPDATE file_collection_contains fcc " +
		"SET child_id=$1 " +
		"WHERE fcc.child_id=$2 " +
		"AND NOT EXISTS (SELECT 1 FROM file_collection_contains WHERE child_id=$1 AND parent_id=fcc.parent_id) " +
		"AND fcc.parent_id<>$1"
	if _, err := tx.Exec(sql,
		newer.FileCollectionID, older.FileCollectionID); err != nil {
		return 0, errors.Wrapf(err, "error moving file_collection_contains from %d to %d\n%s", older.FileCollectionID, newer.FileCollectionID, sql)

	}
	// delete any remaining file_collection_contains
	if _, err := tx.Exec("DELETE FROM file_collection_contains WHERE child_id=$1",
		older.FileCollectionID); err != nil {
		return 0, errors.Wrapf(err, "error moving file_collection_contains from %d to %d", older.FileCollectionID, newer.FileCollectionID)
	}

	// delete old
	if _, err := tx.Exec("DELETE FROM file_belongs_collection WHERE file_collection_id=$1",
		older.FileCollectionID); err != nil {
		return 0, errors.Wrapf(err, "error removing files from old %d", older.FileCollectionID)
	}
	if _, err := tx.Exec("DELETE FROM archive_contains WHERE child_id=$1",
		older.FileCollectionID); err != nil {
		return 0, errors.Wrapf(err, "error deleting archive_contains %d", older.FileCollectionID)
	}
	if _, err := tx.Exec("DELETE FROM file_collection WHERE id=$1",
		older.FileCollectionID); err != nil {
		return 0, errors.Wrapf(err, "error deleting file_collection %d", older.FileCollectionID)
	}

	logger.Debug().Msg("Resolved generic conflict")
	return older.FileCollectionID, nil
}

func resolveSubArchive(conn *sql.Tx, fileCollectionController *TransactionFileCollectionController, newer *file_collection.FileCollection, older *file_collection.FileCollection, fvcOne []byte, fvcTwo []byte) (removed int64, err error) {
	logger := log.With().Str(zerolog.CallerFieldName, "resolveSubArchive").Int64("newer", newer.FileCollectionID).Int64("older", older.FileCollectionID).Hex("fvcOne", fvcOne).Hex("fvcTwo", fvcTwo).Logger()
	logger.Debug().Send()

	// move owned files
	if _, err := conn.Exec("UPDATE file_belongs_collection fbc SET file_collection_id=$1 "+
		"WHERE fbc.file_collection_id=$2 "+
		"AND NOT EXISTS (SELECT 1 FROM file_belongs_collection WHERE file_collection_id=$1 AND file_id=fbc.file_id)",
		newer.FileCollectionID, older.FileCollectionID); err != nil {
		return 0, errors.Wrapf(err, "error moving sub-files")
	}
	// delete remaining
	if _, err := conn.Exec("DELETE FROM file_belongs_collection WHERE file_collection_id=$1",
		older.FileCollectionID); err != nil {
		return 0, errors.Wrapf(err, "error deleting remaining sub-files")
	}

	// move owned sub-collections
	if _, err := conn.Exec("UPDATE file_collection_contains fcc SET parent_id=$1 "+
		"WHERE fcc.parent_id=$2 "+
		"AND NOT EXISTS (SELECT 1 FROM file_collection_contains WHERE parent_id=$1 AND child_id=fcc.child_id)",
		newer.FileCollectionID, older.FileCollectionID); err != nil {
		return 0, errors.Wrapf(err, "error moving sub-collections")
	}
	// delete remaining
	if _, err := conn.Exec("DELETE FROM file_collection_contains WHERE parent_id=$1",
		older.FileCollectionID); err != nil {
		return 0, errors.Wrapf(err, "error deleting remaining sub-files")
	}

	// delete parent/child relationship
	if _, err := conn.Exec("DELETE FROM file_collection_contains WHERE parent_id=$1 AND child_id=$2",
		newer.FileCollectionID, older.FileCollectionID); err != nil {
		return 0, errors.Wrapf(err, "error deleting parent/child relationship")
	}

	// move other parents
	if _, err := conn.Exec("UPDATE file_collection_contains fcc SET child_id=$1 "+
		"WHERE fcc.child_id=$2 "+
		"AND NOT EXISTS (SELECT 1 FROM file_collection_contains WHERE child_id=$1 AND parent_id=fcc.parent_id)",
		newer.FileCollectionID, older.FileCollectionID); err != nil {
		return 0, errors.Wrapf(err, "error moving sub-collections")
	}

	// move archive parents
	if _, err := conn.Exec("UPDATE archive_contains SET child_id=$1 WHERE child_id=$2",
		newer.FileCollectionID, older.FileCollectionID); err != nil {
		return 0, errors.Wrapf(err, "error moving archive parents")
	}

	// delete remaining parents
	if _, err := conn.Exec("DELETE FROM file_collection_contains WHERE child_id=$1",
		older.FileCollectionID); err != nil {
		return 0, errors.Wrapf(err, "error deleting parent relationships")
	}

	// move archives from old to new
	if _, err := conn.Exec("UPDATE archive_table SET file_collection_id=$1 WHERE file_collection_id=$2",
		newer.FileCollectionID, older.FileCollectionID); err != nil {
		return 0, errors.Wrapf(err, "error moving archives from %d to %d", older.FileCollectionID, newer.FileCollectionID)
	}

	// delete collection
	if _, err := conn.Exec("DELETE FROM file_collection WHERE id=$1",
		older.FileCollectionID); err != nil {
		return 0, errors.Wrapf(err, "error deleting file_collection %d", older.FileCollectionID)
	}

	logger.Debug().Msg("Resolved sub-archive")
	return older.FileCollectionID, nil
}

func parseDateTime(dateString string) (*time.Time, error) {
	layouts := []string{"Jan-02-06", "Jan-02-2006", "01/02/2006", "Jan-2-06"}

	var ret time.Time
	var err error
	for _, layout := range layouts {
		ret, err = time.Parse(layout, dateString)
		if err == nil {
			return &ret, nil
		}
	}

	return nil, err
}

type TransactionFileCollectionController struct {
	Tx *sql.Tx
}

var ErrNotFound error = fmt.Errorf("file_collection not found")

func (controller TransactionFileCollectionController) GetByVerificationCode(verificationCode []byte) (*file_collection.FileCollection, error) {
	if len(verificationCode) == 0 {
		return nil, ErrNotFound
	}

	// upgrade verification code if necessary
	version, _ := code.VersionOf(verificationCode)
	if version != nil && *version == code.VERSION_ZERO {
		log.Debug().Str("verification_code", hex.EncodeToString(verificationCode)).Msg("upgrading version zero")
		var err error
		verificationCode, err = legacy.Upgrade(verificationCode)
		if err != nil {
			return nil, err
		}

		v := code.VERSION_ONE
		version = &v
	}
	var query string = "SELECT fc.id, fc.insert_date, fc.group_container_id, fc.flag_extract, fc.flag_license_extracted, fc.license_id, fc.license_rationale, fc.analyst_id, fc.license_expression, fc.license_notice, fc.copyright, fc.verification_code_one, fc.verification_code_two, " +
		"build_group_path(fc.group_container_id) as group_name, " +
		"l.expression as license_expression " +
		"FROM file_collection AS fc " +
		"LEFT JOIN license_expression AS l ON l.id=fc.license_id "

	switch *version {
	case code.VERSION_ONE:
		query += "WHERE fc.verification_code_one=$1 "
	case code.VERSION_TWO:
		query += "WHERE fc.verification_code_two=$1 "
	default:
		return nil, errors.New(fmt.Sprintf("unsupported version: %v\n", version))
	}

	var ret file_collection.FileCollection
	if err := controller.Tx.QueryRow(query, verificationCode).
		Scan(&ret.FileCollectionID, &ret.InsertDate, &ret.GroupID, &ret.Extracted, &ret.LicenseExtracted, &ret.LicenseID, &ret.LicenseRationale, &ret.AnalystID, &ret.LicenseExpression, &ret.LicenseNotice, &ret.Copyright, &ret.VerificationCodeOne, &ret.VerificationCodeTwo,
			&ret.GroupName,
			&ret.LicenseExpression); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}

		return nil, err
	}

	return &ret, nil
}

func (controller TransactionFileCollectionController) GetByID(fileCollectionID int64) (*file_collection.FileCollection, error) {
	if fileCollectionID <= 0 {
		return nil, ErrNotFound
	}

	var ret file_collection.FileCollection
	if err := controller.Tx.QueryRow("SELECT fc.id, fc.insert_date, fc.group_container_id, fc.flag_extract, fc.flag_license_extracted, fc.license_id, fc.license_rationale, fc.analyst_id, fc.license_expression, fc.license_notice, fc.copyright, fc.verification_code_one, fc.verification_code_two, "+
		"build_group_path(fc.group_container_id) as group_name, "+
		"l.expression as license_expression "+
		"FROM file_collection AS fc "+
		"LEFT JOIN license_expression AS l ON l.id=fc.license_id "+
		"WHERE fc.id=$1",
		fileCollectionID).
		Scan(&ret.FileCollectionID, &ret.InsertDate, &ret.GroupID, &ret.Extracted, &ret.LicenseExtracted, &ret.LicenseID, &ret.LicenseRationale, &ret.AnalystID, &ret.LicenseExpression, &ret.LicenseNotice, &ret.Copyright, &ret.VerificationCodeOne, &ret.VerificationCodeTwo,
			&ret.GroupName,
			&ret.LicenseExpression); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}

		return nil, err
	}

	return &ret, nil
}

// // processFileCollection catalogs files found at parentDirectory as children of this archive, and recursively processes any sub-packages.
// // if any file is missing a sha256, the resulting file verification code 2 will be nil
// func (p *TransactionFileCollectionController) CalculateFileCollectionVerificationCode(fileCollectionID int64) (vcodeOne []byte, vcodeTwo []byte, err error) {
// 	vcoderOne := code.NewVersionOne().(*code.VersionOneHasher)
// 	vcoderTwo := code.NewVersionTwo().(*code.VersionTwoHasher)

// 	// Select all files and feed to verification code
// 	rows, err := p.Tx.Query("SELECT f.checksum_sha1, f.checksum_sha256 "+
// 		"FROM file_belongs_collection fbc "+
// 		"INNER JOIN file_table f ON f.id=fbc.file_id "+
// 		"WHERE fbc.file_collection_id=$1 AND flag_symlink=0 AND flag_fifo=0", fileCollectionID)
// 	if err != nil {
// 		return nil, nil, errors.Wrapf(err, "error selecting files of file_collection %d", fileCollectionID)
// 	}
// 	defer rows.Close()

// 	for rows.Next() {
// 		var tmpSha1 sql.NullString
// 		var tmpSha256 sql.NullString
// 		if err := rows.Scan(&tmpSha1, &tmpSha256); err != nil {
// 			return nil, nil, errors.Wrapf(err, "error scanning checksum of files of file_collectior %d", fileCollectionID)
// 		}

// 		if err := vcoderOne.AddSha1Hex(tmpSha1.String); err != nil {
// 			return nil, nil, err
// 		}
// 		if vcoderTwo != nil {
// 			if !tmpSha256.Valid {
// 				vcoderTwo = nil
// 			} else {
// 				if err := vcoderTwo.AddSha256Hex(tmpSha256.String); err != nil {
// 					return nil, nil, err
// 				}
// 			}
// 		}
// 	}
// 	rows.Close()

// 	fcg, err := NewFileCollectionGraph(p.Tx, fileCollectionID)
// 	if err != nil {
// 		return nil, nil, err
// 	}

// 	if len(fcg.Edges) > 0 {
// 		if err := fcg.TraverseUniqueEdges(func(collectionID int64) error {
// 			// Select all files and feed to verification code
// 			rows, err = p.Tx.Query("SELECT f.checksum_sha1, f.checksum_sha256 "+
// 				"FROM file_belongs_collection fbc "+
// 				"INNER JOIN file_table f ON f.id=fbc.file_id "+
// 				"WHERE fbc.file_collection_id=$1", collectionID)
// 			if err != nil {
// 				return errors.Wrapf(err, "error selecting files of collection %d", collectionID)
// 			}
// 			defer rows.Close()

// 			for rows.Next() {
// 				var tmpSha1 sql.NullString
// 				var tmpSha256 sql.NullString
// 				if err := rows.Scan(&tmpSha1, &tmpSha256); err != nil {
// 					return errors.Wrapf(err, "error scanning checksums of files of collection %d", collectionID)
// 				}

// 				if err := vcoderOne.AddSha1Hex(tmpSha1.String); err != nil {
// 					return err
// 				}
// 				if vcoderTwo != nil {
// 					if !tmpSha256.Valid {
// 						vcoderTwo = nil
// 					} else {
// 						if err := vcoderTwo.AddSha256Hex(tmpSha256.String); err != nil {
// 							return err
// 						}
// 					}
// 				}
// 			}

// 			return nil
// 		}); err != nil {
// 			return nil, nil, err
// 		}
// 	}

// 	fvcOne := vcoderOne.Sum()
// 	var fvcTwo []byte
// 	if vcoderTwo != nil {
// 		fvcTwo = vcoderTwo.Sum()
// 	}
// 	return fvcOne, fvcTwo, nil
// }

// processFileCollection catalogs files found at parentDirectory as children of this archive, and recursively processes any sub-packages.
// if any file is missing a sha256, the resulting file verification code 2 will be nil
func (p *TransactionFileCollectionController) CalculateFileCollectionVerificationCode(fileCollectionID int64) (vcodeOne []byte, vcodeTwo []byte, err error) {
	sha1s, sha256s, err := p.CollectShas(fileCollectionID)
	if err != nil {
		return nil, nil, err
	}

	vcoderOne := code.NewVersionOne().(*code.VersionOneHasher)
	var vcoderTwo *code.VersionTwoHasher
	if sha256s != nil {
		vcoderTwo = code.NewVersionTwo().(*code.VersionTwoHasher)
	}

	for _, v := range sha1s {
		if err := vcoderOne.AddSha1Hex(v); err != nil {
			return nil, nil, err
		}
	}
	if vcoderTwo != nil {
		for _, v := range sha256s {
			if err := vcoderTwo.AddSha256Hex(v); err != nil {
				return nil, nil, err
			}
		}
	}

	fvcOne := vcoderOne.Sum()
	var fvcTwo []byte
	if vcoderTwo != nil {
		fvcTwo = vcoderTwo.Sum()
	}

	return fvcOne, fvcTwo, nil
}

func (p *TransactionFileCollectionController) CollectShas(fileCollectionID int64) (sha1s []string, sha256s []string, err error) {
	sha1s = make([]string, 0)
	sha256s = make([]string, 0)

	rows, err := p.Tx.Query(`SELECT f.checksum_sha1, f.checksum_sha256 FROM file_table f
	INNER JOIN file_belongs_collection fbc ON file_id=f.id
	WHERE fbc.file_collection_id=$1`, fileCollectionID)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "error selecting files of file_collection")
	}
	defer rows.Close()

	// collect all direct files
	for rows.Next() {
		var tmpSha1 string
		var tmpSha256 sql.NullString
		if err := rows.Scan(&tmpSha1, &tmpSha256); err != nil {
			return nil, nil, errors.Wrapf(err, "error selecting shas of file")
		}

		sha1s = append(sha1s, tmpSha1)
		if sha256s != nil {
			if tmpSha256.Valid {
				sha256s = append(sha256s, tmpSha256.String)
			} else {
				sha256s = nil
			}
		}
	}
	rows.Close()

	// collect all sub files
	rows, err = p.Tx.Query("SELECT child_id FROM file_collection_contains WHERE parent_id=$1",
		fileCollectionID)
	if err != nil && err != sql.ErrNoRows {
		return nil, nil, errors.Wrapf(err, "error selecting sub collections")
	}
	defer rows.Close()

	subCollections := make([]int64, 0)
	for rows.Next() {
		var tmp int64
		if err := rows.Scan(&tmp); err != nil {
			return nil, nil, errors.Wrapf(err, "error scanning sub collections")
		}

		subCollections = append(subCollections, tmp)
	}
	rows.Close()

	for _, v := range subCollections {
		subSha1s, subSha256s, err := p.CollectShas(v)
		if err != nil {
			return nil, nil, err
		}

		sha1s = append(sha1s, subSha1s...)
		if sha256s != nil && subSha256s != nil {
			sha256s = append(sha256s, subSha256s...)
		}
	}

	return sha1s, sha256s, nil
}

func NewFileCollectionGraph(db *sql.Tx, fileCollectionID int64) (*file_collection.FileCollectionGraph, error) {
	fcg := new(file_collection.FileCollectionGraph)
	fcg.ID = fileCollectionID
	fcg.Edges = make([]int64, 0)
	fcg.Graph = generic.NewDirectedGraph[int64, int64]()

	rows, err := db.Query("SELECT child_id FROM file_collection_contains WHERE parent_id=$1", fileCollectionID)
	if err != nil {
		return nil, errors.Wrapf(err, "error selecting file collection's direct children")
	}
	defer rows.Close()

	for rows.Next() {
		var tmp int64
		if err := rows.Scan(&tmp); err != nil {
			return nil, errors.Wrapf(err, "error scanning file collection's direct children")
		}

		fcg.Edges = append(fcg.Edges, tmp)
		fcg.Graph.Insert(tmp, tmp)
	}
	rows.Close()

	if len(fcg.Edges) > 0 {
		if err := fcg.Graph.TraverseUniqueEdges(func(id int64) error {
			if id == fcg.ID { // skip root node
				return nil
			}

			currentNode := fcg.Graph.Get(id)
			rows, err := db.Query("SELECT child_id FROM file_collection_contains WHERE parent_id=$1", id)
			if err != nil {
				return errors.Wrapf(err, "error selecting file_collection's childern")
			}
			defer rows.Close()

			for rows.Next() {
				var tmp int64
				if err := rows.Scan(&tmp); err != nil {
					return errors.Wrapf(err, "error scanning file_collection's children")
				}

				currentNode.Edges.Add(fcg.Graph.Insert(tmp, tmp))
			}

			return nil
		}, fcg.Edges...); err != nil {
			return nil, err
		}
	}

	return fcg, nil
}
