package commands

import (
	"github.com/diffscope/diffscope-package-manager/packagedatabase/model"
	"github.com/diffscope/diffscope-package-manager/packageinfo"

	"gorm.io/gorm"
)

func packageName(db *gorm.DB, packageID string, version string) (*packageinfo.MultilingualText, error) {
	var rows []model.PackageMultilingualInfo
	err := db.
		Where("package_id = ? AND package_version = ? AND name IS NOT NULL", packageID, version).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return multilingualNameFromPackageRows(rows), nil
}

func inferenceName(db *gorm.DB, packageID string, version string, inferenceID string) (*packageinfo.MultilingualText, error) {
	var rows []model.InferenceMultilingualInfo
	err := db.
		Where("package_id = ? AND package_version = ? AND inference_id = ? AND name IS NOT NULL", packageID, version, inferenceID).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return multilingualNameFromInferenceRows(rows), nil
}

func multilingualNameFromPackageRows(rows []model.PackageMultilingualInfo) *packageinfo.MultilingualText {
	name := packageinfo.MultilingualText{Texts: make(map[string]string)}
	for _, row := range rows {
		if row.Name == nil {
			continue
		}
		addMultilingualName(&name, row.Language, *row.Name)
	}
	return normalizeMultilingualName(name)
}

func multilingualNameFromInferenceRows(rows []model.InferenceMultilingualInfo) *packageinfo.MultilingualText {
	name := packageinfo.MultilingualText{Texts: make(map[string]string)}
	for _, row := range rows {
		if row.Name == nil {
			continue
		}
		addMultilingualName(&name, row.Language, *row.Name)
	}
	return normalizeMultilingualName(name)
}
