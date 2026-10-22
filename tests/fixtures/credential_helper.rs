use std::{
    env, fs,
    io::{self, Read},
    process,
};

fn main() {
    let action = env::args().nth(1).unwrap_or_default();
    if action != "get" {
        process::exit(90);
    }
    let mut context = String::new();
    io::stdin()
        .read_to_string(&mut context)
        .expect("fixture input");
    if !context.contains("protocol=http") {
        process::exit(91);
    }
    let secret =
        fs::read_to_string(env::var_os("GIT_NEXT_TEST_SECRET_FILE").expect("fixture secret path"))
            .expect("fixture secret");
    let (username, password) = secret.split_once('\n').expect("fixture fields");
    println!("username={username}\npassword={}\n", password.trim_end());
    if let Some(log) = env::var_os("GIT_NEXT_TEST_HELPER_LOG") {
        use std::io::Write;
        writeln!(
            fs::OpenOptions::new()
                .create(true)
                .append(true)
                .open(log)
                .expect("fixture log"),
            "get"
        )
        .expect("fixture action");
    }
}
