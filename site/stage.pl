#!/usr/bin/env perl
# Stage the README, CHANGELOG and docs/*.md of a checkout as a flat mdBook
# source, rewriting each relative link that leaves the book to its GitHub URL
# at <ref>. A link to a .md file the repo lacks is an error. Usage:
# stage.pl <repo> <out> <ref>
use strict;
use warnings;
use File::Basename qw(basename);
use File::Path qw(make_path);

my ($repo, $out, $ref) = @ARGV;
die "usage: stage.pl <repo> <out> <ref>\n" unless defined $ref;
my $gh = "https://github.com/elocke/piste/blob/$ref";

# repo-relative source => staged name (docs/superpowers, docs/plans and
# docs/design are subdirectories and so never match the glob: a link to
# docs/design/packs.md, a design not shipped, goes to GitHub, and the book
# leaves it out on purpose). The README is index.md: mdBook
# renders a README.md as index.html but still writes links to it as
# README.html, so it and every link to it get the name of the page it becomes.
my %staged = ('README.md' => 'index.md');
$staged{'CHANGELOG.md'} = 'CHANGELOG.md' if -f "$repo/CHANGELOG.md";
$staged{"docs/" . basename($_)} = basename($_) for glob("$repo/docs/*.md");
die "stage.pl: no README.md in $repo\n" unless -f "$repo/README.md";

# dir + relative path => normalised repo-relative path, or undef if it
# climbs out of the repo
sub resolve {
    my ($dir, $rel) = @_;
    my @out;
    for my $p (split m{/}, ($dir eq '' ? '' : "$dir/") . $rel) {
        next if $p eq '' || $p eq '.';
        if ($p eq '..') { return undef unless @out; pop @out; next }
        push @out, $p;
    }
    return join '/', @out;
}

make_path($out);
for my $src (sort keys %staged) {
    my ($dir) = $src =~ m{^(.*)/[^/]*$};
    $dir //= '';
    open my $in, '<', "$repo/$src" or die "$src: $!";
    open my $o, '>', "$out/$staged{$src}" or die "$staged{$src}: $!";
    my $fence;
    while (my $line = <$in>) {
        if (defined $fence) {
            print $o $line;
            $fence = undef if $line =~ /^ {0,3}\Q$fence\E+\s*$/;
            next;
        }
        if ($line =~ /^ {0,3}(`{3,}|~{3,})/) {
            $fence = substr($1, 0, 1) x length($1);
            print $o $line;
            next;
        }
        # A code span is matched first, so a link inside one is left alone.
        $line =~ s{(`+)(.+?)\1|\]\(([^)\s]+)((?:\s+"[^"]*")?)\)}{
            if (defined $3) {
                my ($target, $title) = ($3, $4);
                my ($path, $frag) = $target =~ /^([^#]*)(#.*)?$/;
                $frag //= '';
                if ($target =~ m{^(?:[A-Za-z][A-Za-z0-9+.-]*:|//|#)} || $path eq '') {
                    "]($target$title)";
                } else {
                    my $res = resolve($dir, $path);
                    if (!defined $res) { die "$src: link $target leaves the repository\n" }
                    if (exists $staged{$res}) { "]($staged{$res}$frag$title)" }
                    elsif ($res =~ /\.md$/ && !-e "$repo/$res") {
                        die "$src: link $target points at $res, which is not in the repository\n";
                    }
                    else { "]($gh/$res$frag$title)" }
                }
            } else {
                $&;
            }
        }ge;
        print $o $line;
    }
    close $o;
}
