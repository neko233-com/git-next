pub(crate) fn check(
    repo: &gix::Repository,
    names: &[&str],
    target: Option<&gix::index::State>,
) -> anyhow::Result<()> {
    crate::hooks::check_lfs_lockable(repo, names, target)?;
    crate::hooks::run_many(repo, names, &[])
}
