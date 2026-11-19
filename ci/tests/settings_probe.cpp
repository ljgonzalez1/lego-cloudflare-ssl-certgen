#include "../../build/src/app.hpp"

int main(int argc, char* argv[]) {
    if (argc != 2 || certgen::configure_cloudflare(60, argv[1]) != 0) {
        return 2;
    }
    return certgen::environment("CF_DNS_API_TOKEN").empty() &&
           certgen::environment("CF_DNS_API_TOKEN_FILE") == argv[1] &&
           certgen::environment("LEGO_DISABLE_CNAME_SUPPORT") == "true" &&
           certgen::environment("CLOUDFLARE_PROPAGATION_TIMEOUT") == "60" ? 0 : 1;
}
