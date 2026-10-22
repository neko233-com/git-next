use gix::{
    bstr::ByteSlice,
    index::entry::{Flags, Mode, Stage, Stat},
};

#[test]
fn directory_lookup_matches_linear_index_paths() {
    for kind in [gix::hash::Kind::Sha1, gix::hash::Kind::Sha256] {
        let mut state = gix::index::State::new(kind);
        let mut paths = vec![
            (b"dir/file".to_vec(), Mode::FILE),
            (b"d".to_vec(), Mode::FILE),
            (b"D/child".to_vec(), Mode::FILE),
            ("文/child".as_bytes().to_vec(), Mode::FILE),
            (b"raw-\xff/child".to_vec(), Mode::FILE),
            (b"module".to_vec(), Mode::COMMIT),
            (b"sparse".to_vec(), Mode::DIR),
        ];
        for length in [63, 64, 65, 127, 128, 129] {
            let mut path = vec![b'a'; length];
            path.extend_from_slice(b"/child");
            paths.push((path, Mode::FILE));
        }
        for (position, (path, mode)) in paths.iter().enumerate() {
            state.dangerously_push_entry(
                Stat::default(),
                kind.null(),
                Flags::from_stage(if position == 0 {
                    Stage::Theirs
                } else {
                    Stage::Unconflicted
                }),
                *mode,
                path.as_bstr(),
            );
        }
        state.sort_entries();
        let lookup = state.prepare_icase_backing();
        let mut queries = vec![Vec::new(), vec![b'z'; 65536]];
        for (path, _) in &paths {
            for length in 0..=path.len() {
                let mut query = path[..length].to_vec();
                queries.push(query.clone());
                query.make_ascii_uppercase();
                queries.push(query);
            }
        }
        for query in queries {
            let directory = query.as_bstr();
            for ignore_case in [false, true] {
                let same = |a: &[u8], b: &[u8]| {
                    if ignore_case {
                        a.eq_ignore_ascii_case(b)
                    } else {
                        a == b
                    }
                };
                let expected = state.entries().iter().any(|entry| {
                    let path = entry.path(&state);
                    ((entry.mode.is_sparse() || entry.mode.is_submodule()) && same(path, directory))
                        || (path.get(directory.len()) == Some(&b'/')
                            && same(&path[..directory.len()], directory))
                });
                let actual = state.entry_closest_to_directory_or_directory_icase(
                    directory,
                    ignore_case,
                    &lookup,
                );
                assert_eq!(
                    actual.is_some(),
                    expected,
                    "directory={directory:?}, ignore_case={ignore_case}"
                );
            }
        }
    }
}
