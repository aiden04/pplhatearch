package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	defaultRepo       = "https://github.com/archlinux/aur.git"
	branchCacheName   = "branches.cache"
	branchCacheMaxAge = 15 * time.Minute
	installedListFile = "installed.txt"

	colorReset   = "\033[0m"
	colorBlue    = "\033[1;34m"
	colorGreen   = "\033[1;32m"
	colorYellow  = "\033[1;33m"
	colorMagenta = "\033[1;35m"
	colorCyan    = "\033[1;36m"
	colorWhite   = "\033[1;37m"
)

var packagePattern = regexp.MustCompile(`^[a-z0-9@._+-]+$`)

func main() {
	os.Args = normalizeShortFlags(os.Args)

	installFlag := flag.Bool("S", false, "install packages (clone branches from the mirror)")
	removeFlag := flag.Bool("R", false, "remove packages (delete local clones)")
	searchFlag := flag.Bool("Ss", false, "search packages in the backup AUR mirror")
	infoFlag := flag.Bool("Si", false, "show package information from the backup AUR mirror")
	queryFlag := flag.Bool("Q", false, "list packages installed via pplhatearch")
	clearCacheFlag := flag.Bool("clear-cache", false, "remove all cached package data and metadata, then exit")
	rootDir := flag.String("C", defaultCacheDir(), "directory to install/remove packages from")
	repoURL := flag.String("repo", defaultRepo, "AUR GitHub mirror to clone from")
	colorFlag := flag.Bool("color", true, "enable colorized output")
	removeNoSave := flag.Bool("n", false, "remove packages without saving configuration files (pacman -n)")
	removeRecursive := flag.Bool("s", false, "remove package dependencies not required elsewhere (pacman -s)")
	removeNodeps := flag.Bool("d", false, "skip dependency checks when removing packages (pacman -d)")
	removeForce := flag.Bool("f", false, "force removal even if dependencies are broken (pacman -f)")

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s (-S|-R) [flags] <package> [<package>...]\n\n", os.Args[0])
		flag.PrintDefaults()
	}

	flag.Parse()

	anyOperation := *installFlag || *removeFlag || *searchFlag || *infoFlag || *queryFlag

	if *clearCacheFlag {
		if anyOperation || flag.NArg() > 0 {
			fmt.Fprintln(os.Stderr, "--clear-cache cannot be combined with other operations or package arguments")
			os.Exit(2)
		}
		ui := newPrinter(os.Stdout, *colorFlag)
		if err := clearCacheDir(*rootDir, ui); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		ui.Pacman("Cache cleared")
		return
	}

	mode, err := determineMode(*installFlag, *removeFlag, *searchFlag, *infoFlag, *queryFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		flag.Usage()
		os.Exit(2)
	}

	packages := flag.Args()

	ui := newPrinter(os.Stdout, *colorFlag)
	inst := newInstaller(*rootDir, *repoURL, ui)

	switch mode {
	case modeSearch:
		if len(packages) == 0 {
			fmt.Fprintln(os.Stderr, "-Ss requires at least one search term")
			os.Exit(2)
		}
		if err := searchPackages(*rootDir, *repoURL, packages, ui); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	case modeInfo:
		if len(packages) == 0 {
			fmt.Fprintln(os.Stderr, "-Si requires at least one package name")
			os.Exit(2)
		}
		for _, pkg := range packages {
			if err := validatePackageName(pkg); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			if err := showPackageInfo(*rootDir, *repoURL, pkg, ui); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
		return
	case modeQuery:
		if err := listInstalledPackages(packages, ui); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	case modeInstall:
		if len(packages) == 0 {
			fmt.Fprintln(os.Stderr, "-S requires at least one package name")
			os.Exit(2)
		}
		ui.AurSummary(packages)
	case modeRemove:
		if len(packages) == 0 {
			fmt.Fprintln(os.Stderr, "-R requires at least one package name")
			os.Exit(2)
		}
	}

	for _, pkg := range packages {
		if err := validatePackageName(pkg); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}

	switch mode {
	case modeInstall:
		ui.Pacman("Installing %d package(s)", len(packages))
		ui.Pacman("Retrieving packages from %s", *repoURL)
	case modeRemove:
		ui.Pacman("Removing %d package(s)", len(packages))
		ui.Pacman("Target directory: %s", filepath.Clean(*rootDir))
	}

	removeOpts := removeOptions{
		noSave:    *removeNoSave,
		recursive: *removeRecursive,
		nodeps:    *removeNodeps,
		force:     *removeForce,
	}

	if mode == modeRemove {
		if err := runPacmanRemove(ui, packages, removeOpts); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	for idx, pkg := range packages {
		var err error

		switch mode {
		case modeInstall:
			ui.PackageProgress("Installing", idx+1, len(packages), pkg)
			err = inst.Install(pkg)
		case modeRemove:
			ui.PackageProgress("Removing cache", idx+1, len(packages), pkg)
			err = removePackage(ui, *rootDir, pkg)
			if err == nil {
				if rmErr := removeInstalledPackage(pkg); rmErr != nil {
					ui.Warn("failed to update installed list for %s: %v", pkg, rmErr)
				}
			}
		}

		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	ui.Pacman("Operation completed")
}

type commandMode int

const (
	modeUnknown commandMode = iota
	modeInstall
	modeRemove
	modeSearch
	modeInfo
	modeQuery
)

func determineMode(install, remove, search, info, query bool) (commandMode, error) {
	selected := []struct {
		flag bool
		mode commandMode
	}{
		{install, modeInstall},
		{remove, modeRemove},
		{search, modeSearch},
		{info, modeInfo},
		{query, modeQuery},
	}

	var mode commandMode
	for _, option := range selected {
		if !option.flag {
			continue
		}
		if mode != modeUnknown {
			return modeUnknown, errors.New("only one primary operation (-S, -R, -Ss, -Si) can be specified at a time")
		}
		mode = option.mode
	}

	if mode == modeUnknown {
		return modeUnknown, errors.New("one of -S, -R, -Ss, -Si, or -Q must be specified")
	}

	return mode, nil
}

func validatePackageName(name string) error {
	if name == "" {
		return errors.New("package name cannot be empty")
	}
	if !packagePattern.MatchString(name) {
		return fmt.Errorf("invalid package name %q", name)
	}
	return nil
}

type installer struct {
	root        string
	repo        string
	ui          *printer
	building    map[string]bool
	completed   map[string]bool
	reader      *bufio.Reader
	interactive bool
}

func newInstaller(root, repo string, ui *printer) *installer {
	var interactive bool
	if os.Stdin != nil {
		interactive = isTerminal(os.Stdin)
	}
	return &installer{
		root:        filepath.Clean(root),
		repo:        repo,
		ui:          ui,
		building:    make(map[string]bool),
		completed:   make(map[string]bool),
		reader:      bufio.NewReader(os.Stdin),
		interactive: interactive,
	}
}

func (in *installer) Install(pkg string) error {
	pkg = strings.TrimSpace(pkg)
	if pkg == "" {
		return errors.New("package name cannot be empty")
	}
	if in.completed[pkg] {
		return nil
	}
	if in.building[pkg] {
		return fmt.Errorf("dependency cycle detected involving %s", pkg)
	}

	in.building[pkg] = true
	defer delete(in.building, pkg)

	root := in.root
	if err := ensureRoot(root); err != nil {
		return err
	}
	dest := filepath.Join(root, pkg)

	if exists(dest) && in.interactive {
		clean, err := in.promptCleanBuild(pkg)
		if err != nil {
			return err
		}
		if clean {
			in.ui.Makepkg("Cleaning build directory for %s", pkg)
			if err := os.RemoveAll(dest); err != nil {
				return fmt.Errorf("failed to clean %s: %w", dest, err)
			}
		}
	}

	var err error
	if exists(dest) {
		in.ui.Makepkg("Updating %s cache", pkg)
		err = updatePackage(dest, pkg)
	} else {
		in.ui.Makepkg("Cloning %s from %s", pkg, in.repo)
		err = clonePackage(dest, pkg, in.repo)
		if err == nil {
			in.ui.Pacman("Downloaded PKGBUILD: %s", pkg)
		}
	}
	if err != nil {
		return err
	}

	if in.interactive && exists(dest) {
		if err := in.promptDiff(pkg, dest); err != nil {
			return err
		}
	}

	deps, err := resolveDependencies(dest)
	if err != nil {
		return err
	}

	for _, dep := range deps {
		if dep == pkg {
			continue
		}
		if isPackageInstalled(dep) {
			continue
		}
		if isOfficialPackage(dep) {
			in.ui.Makepkg("Dependency %s will be installed via pacman", dep)
			continue
		}
		in.ui.Makepkg("Resolving AUR dependency %s", dep)
		if err := in.Install(dep); err != nil {
			return err
		}
	}

	if err := runMakepkg(dest, pkg, in.ui); err != nil {
		return err
	}

	in.recordInstall(pkg)
	in.completed[pkg] = true
	return nil
}

func (in *installer) promptCleanBuild(pkg string) (bool, error) {
	if !in.interactive {
		return false, nil
	}
	fmt.Println()
	in.printPackageLine(pkg)
	fmt.Println("==> Packages to cleanBuild?")
	fmt.Println("==> [N]one [A]ll [Ab]ort [I]nstalled [No]tInstalled or (1 2 3, 1-3, ^4)")
	fmt.Print("==> ")
	resp, err := in.readInput()
	if err != nil {
		return false, err
	}
	switch strings.ToLower(resp) {
	case "a", "all":
		return true, nil
	case "ab", "abort":
		return false, fmt.Errorf("operation aborted by user")
	case "", "n", "none":
		return false, nil
	default:
		return false, nil
	}
}

func (in *installer) promptDiff(pkg, dest string) error {
	if !in.interactive {
		return nil
	}
	fmt.Println()
	in.printPackageLine(pkg)
	fmt.Println("==> Diffs to show?")
	fmt.Println("==> [N]one [A]ll [Ab]ort [I]nstalled [No]tInstalled or (1 2 3, 1-3, ^4)")
	fmt.Print("==> ")
	resp, err := in.readInput()
	if err != nil {
		return err
	}
	switch strings.ToLower(resp) {
	case "a", "all":
		return in.showDiff(dest)
	case "ab", "abort":
		return fmt.Errorf("operation aborted by user")
	default:
		return nil
	}
}

func (in *installer) showDiff(dest string) error {
	cmd := exec.Command("git", "diff")
	cmd.Dir = dest
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git diff failed in %s: %w", dest, err)
	}
	return nil
}

func (in *installer) readInput() (string, error) {
	if in.reader == nil {
		in.reader = bufio.NewReader(os.Stdin)
	}
	line, err := in.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func (in *installer) printPackageLine(pkg string) {
	fmt.Fprintf(os.Stdout, "  1 %-32s (Build Files Exist)\n", pkg)
}

func (in *installer) recordInstall(pkg string) {
	if err := addInstalledPackage(pkg); err != nil {
		in.ui.Warn("failed to record installation for %s: %v", pkg, err)
	}
}

func removePackage(ui *printer, root, pkg string) error {
	root = filepath.Clean(root)
	dest := filepath.Join(root, pkg)

	info, err := os.Stat(dest)
	if err != nil {
		if os.IsNotExist(err) {
			ui.Makepkg("Cache for %s not found, skipping directory removal", pkg)
			return nil
		}
		return fmt.Errorf("failed to stat %s: %w", dest, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s exists but is not a directory", dest)
	}

	ui.Makepkg("Removing %s", dest)

	if err := os.RemoveAll(dest); err != nil {
		return fmt.Errorf("failed to remove %s: %w", dest, err)
	}
	ui.Makepkg("Removed %s", pkg)
	return nil
}

type removeOptions struct {
	noSave    bool
	recursive bool
	nodeps    bool
	force     bool
}

func runPacmanRemove(ui *printer, packages []string, opts removeOptions) error {
	if len(packages) == 0 {
		return nil
	}

	args := []string{"-R"}
	if opts.noSave {
		args = append(args, "-n")
	}
	if opts.recursive {
		args = append(args, "-s")
	}
	if opts.nodeps {
		args = append(args, "-d")
	}
	if opts.force {
		args = append(args, "-f")
	}
	args = append(args, packages...)

	ui.Makepkg("Running pacman %s", strings.Join(args, " "))

	cmd := exec.Command("pacman", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pacman remove failed: %w", err)
	}
	return nil
}

func ensureRoot(root string) error {
	if root == "." {
		return nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("failed to create root directory %s: %w", root, err)
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func clonePackage(dest, pkg, repo string) error {
	cmd := exec.Command(
		"git",
		"clone",
		"--branch", pkg,
		"--single-branch",
		repo,
		dest,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone failed for %s: %w", pkg, err)
	}
	return nil
}

func updatePackage(dest, pkg string) error {
	cmds := []*exec.Cmd{
		exec.Command("git", "fetch", "origin", pkg),
		exec.Command("git", "checkout", pkg),
		exec.Command("git", "reset", "--hard", fmt.Sprintf("origin/%s", pkg)),
	}

	for i, cmd := range cmds {
		cmd.Dir = dest
		quiet := i > 0
		if err := runCommand(cmd, quiet); err != nil {
			return fmt.Errorf("git update failed in %s: %w", dest, err)
		}
	}
	return nil
}

func resolveDependencies(dest string) ([]string, error) {
	data, err := generateSRCINFO(dest)
	if err != nil {
		return nil, err
	}
	values := parseSRCINFO(data)
	return extractDependencies(values), nil
}

func generateSRCINFO(dest string) ([]byte, error) {
	cmd := exec.Command("makepkg", "--printsrcinfo")
	cmd.Dir = dest
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to inspect %s: %w", dest, err)
	}
	return stdout.Bytes(), nil
}

func parseSRCINFO(data []byte) map[string][]string {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	values := make(map[string][]string)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		values[key] = append(values[key], value)
	}

	return values
}

func extractDependencies(values map[string][]string) []string {
	deps := []string{}
	seen := make(map[string]struct{})
	for _, key := range []string{"depends", "makedepends"} {
		for _, item := range values[key] {
			name := cleanDependencyName(item)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			deps = append(deps, name)
		}
	}
	return deps
}

func cleanDependencyName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	if value == "" {
		return ""
	}
	if idx := strings.IndexAny(value, "<>="); idx != -1 {
		value = strings.TrimSpace(value[:idx])
	}
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	value = fields[0]
	if strings.HasSuffix(value, ".so") {
		return ""
	}
	return value
}

func isPackageInstalled(name string) bool {
	cmd := exec.Command("pacman", "-Qi", name)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}

func isOfficialPackage(name string) bool {
	cmd := exec.Command("pacman", "-Si", name)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}

func runMakepkg(dest, pkg string, ui *printer) error {
	ui.Makepkg("Building %s with makepkg", pkg)
	cmd := exec.Command("makepkg", "-si")
	cmd.Dir = dest
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("makepkg failed for %s: %w", pkg, err)
	}
	ui.Makepkg("Installed %s", pkg)
	return nil
}

type printer struct {
	out        io.Writer
	enableANSI bool
}

func newPrinter(out io.Writer, colorEnabled bool) *printer {
	useColor := false
	if colorEnabled {
		useColor = true
		if outFile, ok := out.(*os.File); ok {
			useColor = isTerminal(outFile)
		}
	}
	return &printer{out: out, enableANSI: useColor}
}

func (p *printer) Pacman(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(p.out, "%s %s\n", p.style("::"), p.colorize(msg, colorCyan))
}

func (p *printer) PackageProgress(action string, idx, total int, pkg string) {
	act := p.colorize(action, colorYellow)
	target := p.colorize(pkg, colorGreen)
	fmt.Fprintf(p.out, "%s (%d/%d) %s %s...\n", p.style("::"), idx, total, act, target)
}

func (p *printer) Makepkg(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(p.out, "%s %s\n", p.style("==>"), p.colorize(msg, colorGreen))
}

func (p *printer) Warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(p.out, "%s %s\n", p.style("::"), p.colorize(msg, colorYellow))
}

func (p *printer) AurSummary(pkgs []string) {
	if len(pkgs) == 0 {
		return
	}
	colored := make([]string, len(pkgs))
	for i, pkg := range pkgs {
		colored[i] = p.colorize(pkg, colorMagenta)
	}
	header := p.colorize(fmt.Sprintf("AUR Explicit (%d):", len(pkgs)), colorWhite)
	fmt.Fprintf(p.out, "%s %s\n", header, strings.Join(colored, " "))
}

func searchPackages(root, repo string, terms []string, ui *printer) error {
	refs, err := loadBranchCache(root, repo, ui)
	if err != nil {
		return err
	}

	lowerTerms := make([]string, len(terms))
	for i, term := range terms {
		lowerTerms[i] = strings.ToLower(term)
	}

	found := false
	for _, ref := range refs {
		match := true
		lowerName := strings.ToLower(ref.name)
		for _, term := range lowerTerms {
			if !strings.Contains(lowerName, term) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		found = true
		fmt.Fprintf(ui.out, "%s %s\n",
			ui.colorize("aur/"+ref.name, colorMagenta),
			ui.colorize(ref.hash[:8], colorYellow))
	}
	if !found {
		ui.Pacman("No packages matched the search criteria.")
	}
	return nil
}

type branchRef struct {
	name string
	hash string
}

func loadBranchCache(root, repo string, ui *printer) ([]branchRef, error) {
	root = filepath.Clean(root)
	cacheFile := filepath.Join(root, branchCacheName)

	useCache := false
	if info, err := os.Stat(cacheFile); err == nil {
		if time.Since(info.ModTime()) < branchCacheMaxAge {
			useCache = true
		}
	}

	var data []byte
	var err error
	if useCache {
		data, err = os.ReadFile(cacheFile)
		if err != nil {
			useCache = false
		}
	}

	if !useCache {
		if err := ensureRoot(root); err != nil {
			return nil, err
		}
		ui.Makepkg("Refreshing branch cache from %s", repo)
		data, err = fetchBranchList(repo)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(cacheFile, data, 0o644); err != nil {
			return nil, fmt.Errorf("failed to write branch cache: %w", err)
		}
	}

	return parseBranchList(data), nil
}

func fetchBranchList(repo string) ([]byte, error) {
	cmd := exec.Command("git", "ls-remote", "--heads", repo)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to query %s: %w", repo, err)
	}
	return stdout.Bytes(), nil
}

func parseBranchList(data []byte) []branchRef {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	refs := []branchRef{}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		ref := fields[1]
		if !strings.HasPrefix(ref, "refs/heads/") {
			continue
		}
		name := strings.TrimPrefix(ref, "refs/heads/")
		refs = append(refs, branchRef{name: name, hash: fields[0]})
	}
	return refs
}

type packageInfo struct {
	Base        string
	Name        string
	Version     string
	Description string
	URL         string
	Depends     []string
	MakeDepends []string
	Provides    []string
	License     []string
}

func showPackageInfo(root, repo, pkg string, ui *printer) error {
	dest, err := ensurePackageFetched(root, repo, pkg, ui)
	if err != nil {
		return err
	}
	data, err := generateSRCINFO(dest)
	if err != nil {
		return err
	}
	values := parseSRCINFO(data)
	info := buildPackageInfo(values, pkg)
	printPackageInfo(info)
	return nil
}

func ensurePackageFetched(root, repo, pkg string, ui *printer) (string, error) {
	root = filepath.Clean(root)
	dest := filepath.Join(root, pkg)
	if err := ensureRoot(root); err != nil {
		return "", err
	}
	var err error
	if exists(dest) {
		ui.Makepkg("Updating %s cache", pkg)
		err = updatePackage(dest, pkg)
	} else {
		ui.Makepkg("Cloning %s from %s", pkg, repo)
		err = clonePackage(dest, pkg, repo)
	}
	if err != nil {
		return "", err
	}
	return dest, nil
}

func buildPackageInfo(values map[string][]string, fallbackName string) packageInfo {
	info := packageInfo{}

	if v := values["pkgbase"]; len(v) > 0 {
		info.Base = v[0]
	}
	if v := values["pkgname"]; len(v) > 0 {
		info.Name = v[0]
	} else {
		info.Name = fallbackName
	}
	ver := ""
	if v := values["pkgver"]; len(v) > 0 {
		ver = v[0]
	}
	if r := values["pkgrel"]; len(r) > 0 {
		if ver != "" {
			ver = fmt.Sprintf("%s-%s", ver, r[0])
		} else {
			ver = r[0]
		}
	}
	info.Version = ver
	if v := values["pkgdesc"]; len(v) > 0 {
		info.Description = v[0]
	}
	if v := values["url"]; len(v) > 0 {
		info.URL = v[0]
	}
	info.Depends = append(info.Depends, values["depends"]...)
	info.MakeDepends = append(info.MakeDepends, values["makedepends"]...)
	info.Provides = append(info.Provides, values["provides"]...)
	info.License = append(info.License, values["license"]...)

	return info
}

func printPackageInfo(info packageInfo) {
	fmt.Printf("Repository      : aur\n")
	fmt.Printf("Name            : %s\n", info.Name)
	if info.Base != "" && info.Base != info.Name {
		fmt.Printf("Package Base    : %s\n", info.Base)
	}
	fmt.Printf("Version         : %s\n", info.Version)
	if info.Description != "" {
		fmt.Printf("Description     : %s\n", info.Description)
	}
	if info.URL != "" {
		fmt.Printf("URL             : %s\n", info.URL)
	}
	fmt.Printf("Depends On      : %s\n", strings.Join(emptyIfNil(info.Depends), " "))
	fmt.Printf("Make Deps       : %s\n", strings.Join(emptyIfNil(info.MakeDepends), " "))
	if len(info.Provides) > 0 {
		fmt.Printf("Provides        : %s\n", strings.Join(info.Provides, " "))
	}
	if len(info.License) > 0 {
		fmt.Printf("License         : %s\n", strings.Join(info.License, " "))
	}
	fmt.Println()
}

func emptyIfNil(values []string) []string {
	if len(values) == 0 {
		return []string{"None"}
	}
	return values
}

func runCommand(cmd *exec.Cmd, quiet bool) error {
	if quiet {
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func (p *printer) colorize(text, color string) string {
	if !p.enableANSI || text == "" {
		return text
	}
	return color + text + colorReset
}

func clearCacheDir(root string, ui *printer) error {
	root = filepath.Clean(root)
	if root == "." || root == "/" {
		return fmt.Errorf("refusing to clear cache root %s", root)
	}
	ui.Makepkg("Clearing cache in %s", root)
	if _, err := os.Stat(root); os.IsNotExist(err) {
		ui.Makepkg("Cache directory %s does not exist, nothing to clean", root)
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", root, err)
	}
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("failed to remove %s: %w", path, err)
		}
	}
	return nil
}

func listInstalledPackages(filters []string, ui *printer) error {
	pkgs, err := loadInstalledPackages()
	if err != nil {
		return fmt.Errorf("failed to read installed package list: %w", err)
	}
	if len(pkgs) == 0 {
		ui.Pacman("No packages have been recorded yet.")
		return nil
	}

	filterSet := make(map[string]struct{})
	if len(filters) > 0 {
		for _, pkg := range filters {
			if err := validatePackageName(pkg); err != nil {
				return err
			}
			filterSet[pkg] = struct{}{}
		}
	}

	count := 0
	for _, pkg := range pkgs {
		if len(filterSet) > 0 {
			if _, ok := filterSet[pkg]; !ok {
				continue
			}
		}
		fmt.Fprintln(ui.out, ui.colorize(pkg, colorMagenta))
		count++
	}
	if count == 0 {
		ui.Pacman("No packages matched the query.")
	}
	return nil
}

func addInstalledPackage(pkg string) error {
	pkgs, err := loadInstalledPackages()
	if err != nil {
		return err
	}
	for _, existing := range pkgs {
		if existing == pkg {
			return nil
		}
	}
	pkgs = append(pkgs, pkg)
	return saveInstalledPackages(pkgs)
}

func removeInstalledPackage(pkg string) error {
	pkgs, err := loadInstalledPackages()
	if err != nil {
		return err
	}
	if len(pkgs) == 0 {
		return nil
	}
	filtered := pkgs[:0]
	for _, existing := range pkgs {
		if existing == pkg {
			continue
		}
		filtered = append(filtered, existing)
	}
	if len(filtered) == len(pkgs) {
		return nil
	}
	return saveInstalledPackages(filtered)
}

func loadInstalledPackages() ([]string, error) {
	path := installedListPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	pkgs := []string{}
	seen := make(map[string]struct{})
	for scanner.Scan() {
		name := strings.TrimSpace(scanner.Text())
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		pkgs = append(pkgs, name)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	sort.Strings(pkgs)
	return pkgs, nil
}

func saveInstalledPackages(pkgs []string) error {
	path := installedListPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if len(pkgs) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	sort.Strings(pkgs)
	content := strings.Join(pkgs, "\n") + "\n"
	return os.WriteFile(path, []byte(content), 0o644)
}

func (p *printer) style(prefix string) string {
	if !p.enableANSI {
		return prefix
	}

	switch prefix {
	case "::":
		return colorBlue + "::" + colorReset
	case "==>":
		return colorGreen + "==>" + colorReset
	default:
		return prefix
	}
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func defaultCacheDir() string {
	if override := os.Getenv("PPLHATEARCH_CACHE"); override != "" {
		return override
	}
	if cacheHome := os.Getenv("XDG_CACHE_HOME"); cacheHome != "" {
		return filepath.Join(cacheHome, "pplhatearch")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".cache", "pplhatearch")
	}
	return "./.pplhatearch-cache"
}

func normalizeShortFlags(argv []string) []string {
	if len(argv) <= 1 {
		return argv
	}

	combined := map[string]struct{}{
		"-Ss": {},
		"-Si": {},
	}

	stackable := map[rune]bool{
		'S': true,
		'R': true,
		'n': true,
		's': true,
		'd': true,
		'f': true,
	}

	out := []string{argv[0]}
	for _, arg := range argv[1:] {
		if _, ok := combined[arg]; ok {
			out = append(out, arg)
			continue
		}
		if !strings.HasPrefix(arg, "-") || len(arg) <= 2 || strings.HasPrefix(arg, "--") || strings.Contains(arg, "=") {
			out = append(out, arg)
			continue
		}
		runes := []rune(arg[1:])
		canExpand := true
		for _, r := range runes {
			if !stackable[r] {
				canExpand = false
				break
			}
		}
		if !canExpand {
			out = append(out, arg)
			continue
		}
		for _, r := range runes {
			out = append(out, "-"+string(r))
		}
	}
	return out
}

func stateDir() string {
	if override := os.Getenv("PPLHATEARCH_STATE"); override != "" {
		return filepath.Clean(override)
	}
	if stateHome := os.Getenv("XDG_STATE_HOME"); stateHome != "" {
		return filepath.Join(stateHome, "pplhatearch")
	}
	if home := preferredHome(); home != "" {
		return filepath.Join(home, ".local", "state", "pplhatearch")
	}
	return "./.pplhatearch-state"
}

func installedListPath() string {
	return filepath.Join(stateDir(), installedListFile)
}

func preferredHome() string {
	if os.Geteuid() == 0 {
		if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" {
			if u, err := user.Lookup(sudoUser); err == nil && u.HomeDir != "" {
				return u.HomeDir
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	if home := os.Getenv("HOME"); home != "" {
		return home
	}
	return ""
}
