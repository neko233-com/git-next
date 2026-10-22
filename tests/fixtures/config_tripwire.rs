use std::{env, fs::OpenOptions, io::Write};

fn main() {
    let marker = env::var_os("GIT_NEXT_CONFIG_TRIPWIRE").expect("fixture marker");
    let mut file = OpenOptions::new().create(true).append(true).open(marker).expect("fixture marker open");
    writeln!(file, "invoked").expect("fixture marker write");
    if env::args().any(|argument| argument == "--exec-path") {
        println!("{}", env::var("GIT_NEXT_FIXTURE_CORE").expect("fixture core"));
    } else {
        print!("system\0file:{}\0probe.source\0", env::var("GIT_NEXT_FIXTURE_CONFIG").expect("fixture config"));
    }
}
