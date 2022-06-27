package template

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/pkg/errors"

	"github.com/suborbital/atmo/directive"
	"github.com/suborbital/velo/cli/util"
)

// ErrTemplateMissing and others are template related errors.
var ErrTemplateMissing = errors.New("template missing")

type tmplData struct {
	directive.Runnable
	NameCaps  string
	NameCamel string
}

func UpdateTemplates(repo, branch string) (string, error) {
	util.LogStart("downloading templates")

	repoParts := strings.Split(repo, "/")
	if len(repoParts) != 2 {
		return "", fmt.Errorf("repo is invalid, contains %d parts", len(repoParts))
	}

	repoName := repoParts[1]

	branchDirName := fmt.Sprintf("%s-%s", repoName, strings.ReplaceAll(branch, "/", "-"))

	templateRootPath, err := TemplateRootDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to TemplateDir")
	}

	filepathVar, err := downloadZip(repo, branch, templateRootPath)
	if err != nil {
		return "", errors.Wrap(err, "🚫 failed to downloadZip for templates")
	}

	// The tmplPath may be different than the default if a custom URL was provided.
	tmplPath, err := extractZip(filepathVar, templateRootPath, branchDirName)
	if err != nil {
		return "", errors.Wrap(err, "🚫 failed to extractZip for templates")
	}

	util.LogDone("templates downloaded")

	return tmplPath, nil
}

// TemplatesExist returns the templates directory for the provided repo and branch.
func TemplatesExist(repo, branch string) (string, error) {
	repoParts := strings.Split(repo, "/")
	if len(repoParts) != 2 {
		return "", fmt.Errorf("repo is invalid, contains %d parts", len(repoParts))
	}

	repoName := repoParts[1]

	templateRootPath, err := TemplateRootDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to TemplateDir")
	}

	branchDirName := fmt.Sprintf("%s-%s", repoName, strings.ReplaceAll(branch, "/", "-"))
	existingPath := filepath.Join(templateRootPath, branchDirName)

	tmplPath := filepath.Join(existingPath, "templates")

	if files, err := os.ReadDir(tmplPath); err != nil {
		return "", errors.Wrap(err, "failed to ReadDir")
	} else if len(files) == 0 {
		return "", errors.New("templates directory is empty")
	}

	return tmplPath, nil
}

// ExecRunnableTmplStr executes a template string with the runnable's data.
func ExecRunnableTmplStr(templateStr string, runnable *directive.Runnable) (string, error) {
	templateData := makeTemplateData(runnable)

	tmpl, err := template.New("tmpl").Parse(templateStr)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse template string")
	}

	builder := &strings.Builder{}
	if err := tmpl.Execute(builder, templateData); err != nil {
		return "", errors.Wrap(err, "failed to Execute template")
	}

	return builder.String(), nil
}

// ExecRunnableTmpl copies a template.
func ExecRunnableTmpl(cwd, name, templatesPath string, tmplName string, runnable *directive.Runnable) error {
	templateData := makeTemplateData(runnable)

	return ExecTmplDir(cwd, name, templatesPath, tmplName, templateData)
}

// ExecTmplDir copies a generic templated directory.
func ExecTmplDir(cwd, name, templatesPath, tmplName string, templateData interface{}) error {
	templatePath := filepath.Join(templatesPath, tmplName)
	targetPath := filepath.Join(cwd, name)

	if _, err := os.Stat(templatePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrTemplateMissing
		}

		return errors.Wrap(err, "failed to Stat template directory")
	}

	var err = filepath.Walk(templatePath, func(path string, info os.FileInfo, _ error) error {
		var relPath = strings.Replace(path, templatePath, "", 1)
		if relPath == "" {
			return nil
		}

		targetRelPath := relPath
		if strings.Contains(relPath, ".tmpl") {
			tmpl, err := template.New("tmpl").Parse(strings.Replace(relPath, ".tmpl", "", -1))
			if err != nil {
				return errors.Wrapf(err, "failed to parse template directory name %s", info.Name())
			}

			builder := &strings.Builder{}
			if err := tmpl.Execute(builder, templateData); err != nil {
				return errors.Wrapf(err, "failed to Execute template for %s", info.Name())
			}

			targetRelPath = builder.String()
		}

		// Check if the target path is an existing file, and skip it if so.
		if _, err := os.Stat(filepath.Join(targetPath, targetRelPath)); err != nil {
			if os.IsNotExist(err) {
				// That's fine, continue.
			} else {
				return errors.Wrap(err, "failed to Stat")
			}
		} else {
			// If the target file already exists, we're going to skip the rest since we don't want to overwrite.
			return nil
		}

		if info.IsDir() {
			if err := os.Mkdir(filepath.Join(targetPath, targetRelPath), util.PermDirectory); err != nil {
				return errors.Wrap(err, "failed to Mkdir")
			}

			return nil
		}

		var data, err1 = ioutil.ReadFile(filepath.Join(templatePath, relPath))
		if err1 != nil {
			return err1
		}

		if strings.HasSuffix(info.Name(), ".tmpl") {
			tmpl, err := template.New("tmpl").Parse(string(data))
			if err != nil {
				return errors.Wrapf(err, "failed to parse template file %s", info.Name())
			}

			builder := &strings.Builder{}
			if err := tmpl.Execute(builder, templateData); err != nil {
				return errors.Wrapf(err, "failed to Execute template for %s", info.Name())
			}

			data = []byte(builder.String())
		}

		if err := ioutil.WriteFile(filepath.Join(targetPath, targetRelPath), data, util.PermFilePrivate); err != nil {
			return errors.Wrap(err, "failed to WriteFile")
		}

		return nil
	})

	return err
}

// downloadZip downloads a ZIP from a particular branch of the Subo repo.
func downloadZip(repo, branch, targetPath string) (string, error) {
	url := fmt.Sprintf("https://github.com/%s/archive/%s.zip", repo, branch)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", errors.Wrap(err, "failed to NewRequest")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "failed to Do request")
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("response was non-200: %d", resp.StatusCode)
	}

	filepathVar := filepath.Join(targetPath, "subo.zip")

	// Check if the zip already exists, and delete it if it does.
	if _, err := os.Stat(filepathVar); err == nil {
		if err := os.Remove(filepathVar); err != nil {
			return "", errors.Wrap(err, "failed to delete exising templates zip")
		}
	}

	if err := os.MkdirAll(targetPath, util.PermDirectory); err != nil {
		return "", errors.Wrap(err, "failed to MkdirAll")
	}

	file, err := os.Create(filepathVar)
	if err != nil {
		return "", errors.Wrap(err, "failed to Open file")
	}

	defer resp.Body.Close()
	if _, err := io.Copy(file, resp.Body); err != nil {
		return "", errors.Wrap(err, "failed to Copy data to file")
	}

	return filepathVar, nil
}

// extractZip extracts a ZIP file.
func extractZip(filePath, destPath, branchDirName string) (string, error) {
	escapedFilepath := strings.ReplaceAll(filePath, " ", "\\ ")
	escapedDestPath := strings.ReplaceAll(destPath, " ", "\\ ") + string(filepath.Separator)

	existingPath := filepath.Join(destPath, branchDirName)

	if _, err := os.Stat(existingPath); err == nil {
		if err := os.RemoveAll(existingPath); err != nil {
			return "", errors.Wrap(err, "failed to RemoveAll old templates")
		}
	}

	if _, err := util.Command.Run(fmt.Sprintf("unzip -q %s -d %s", escapedFilepath, escapedDestPath)); err != nil {
		return "", errors.Wrap(err, "failed to Run unzip")
	}

	return filepath.Join(existingPath, "templates"), nil
}

// makeTemplateData makes data to be used in templates.
func makeTemplateData(runnable *directive.Runnable) tmplData {
	nameCamel := ""
	nameParts := strings.Split(runnable.Name, "-")
	for _, part := range nameParts {
		nameCamel += strings.ToUpper(string(part[0]))
		nameCamel += string(part[1:])
	}

	return tmplData{
		Runnable:  *runnable,
		NameCaps:  strings.ToUpper(strings.Replace(runnable.Name, "-", "", -1)),
		NameCamel: nameCamel,
	}
}
