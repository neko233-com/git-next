# Copyright 2026 git-next contributors. SPDX-License-Identifier: GPL-2.0-only
# Resolve modules from the owned helper layout, including direct invocation.
BEGIN {
	require FindBin;
	require File::Spec;
	require File::Basename;
	my $exec_path = $ENV{GIT_EXEC_PATH} || $FindBin::RealBin;
	my @parents = File::Basename::basename($exec_path) eq 'bin' ? ('..') : ('..', '..');
	my $share = File::Spec->catdir($exec_path, @parents, 'share');
	unshift @INC, $ENV{GITPERLLIB} ? split(/:/, $ENV{GITPERLLIB}) : File::Spec->catdir($share, 'perl5');
	$Git::I18N::TEXTDOMAINDIR = File::Spec->catdir($share, 'locale');
}
