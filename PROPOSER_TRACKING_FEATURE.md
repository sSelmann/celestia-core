# Proposer Tracking Feature / Round Proposer Bilgisi İzleme Özelliği

## Genel Bakış / Overview

Bu özellik, consensus sürecinde her round'da kimlerin proposer olduğunu ve bu proposer'ların block'u başarıyla propose edip etmediğini takip eder ve blockchain'e kaydeder.

This feature tracks which validators were proposers in each round during consensus and whether they successfully proposed a block, recording this information in the blockchain.

## Yapılan Değişiklikler / Changes Made

### 1. Protobuf Tanımları / Protobuf Definitions
**Dosya / File:** `proto/tendermint/types/types.proto`

Yeni mesaj tipi eklendi:
```protobuf
message ProposerRoundInfo {
  int32 round            = 1;
  bytes proposer_address = 2;
  bool  proposed         = 3;  // true if proposer successfully proposed a block in this round
}
```

`Commit` ve `ExtendedCommit` mesajlarına yeni alan eklendi:
```protobuf
repeated ProposerRoundInfo round_proposers = 5;
```

### 2. Go Type Tanımları / Go Type Definitions
**Dosya / File:** `types/block.go`

- `ProposerRoundInfo` struct'ı eklendi
- `Commit` ve `ExtendedCommit` struct'larına `RoundProposers []ProposerRoundInfo` alanı eklendi
- İlgili ToProto(), FromProto(), Clone(), ValidateBasic() metodları güncellendi

### 3. Consensus State Güncellemeleri / Consensus State Updates
**Dosya / File:** `consensus/state.go`

- `State` struct'ına `roundProposers map[int32]*types.ProposerRoundInfo` alanı eklendi
- `enterNewRound()`: Her yeni round başladığında proposer bilgisi kaydediliyor
- `defaultSetProposal()`: Proposal alındığında `Proposed = true` olarak işaretleniyor
- `finalizeCommit()`: Block commit edilirken tüm round proposer bilgileri commit'e ekleniyor
- `updateToState()`: Yeni height başladığında roundProposers map'i sıfırlanıyor

### 4. Validation / Doğrulama

- `Commit.ValidateBasic()` ve `ExtendedCommit.ValidateBasic()` metodlarında roundProposers doğrulaması eklendi
- Round number ve proposer address uzunluğu kontrolleri yapılıyor

### 5. Backward Compatibility / Geriye Uyumluluk

✅ **Commit.Hash()** metodu sadece signatures'ları kullandığı için roundProposers hash'e dahil edilmiyor
✅ Mevcut block hash'leri değişmiyor
✅ Eski blocklar için roundProposers boş array olarak geliyor
✅ ValidateBasic() boş roundProposers array'ine izin veriyor

## Kullanım / Usage

### RPC Üzerinden Erişim / Access via RPC

Commit bilgilerini döndüren RPC endpoint'leri otomatik olarak roundProposers bilgisini de döndürür:

```bash
# Belirli bir height için commit bilgisi
curl http://localhost:26657/commit?height=100

# Response örneği:
{
  "result": {
    "signed_header": {
      "header": { ... },
      "commit": {
        "height": "100",
        "round": 2,
        "block_id": { ... },
        "signatures": [ ... ],
        "round_proposers": [
          {
            "round": 0,
            "proposer_address": "ABCD1234...",
            "proposed": false
          },
          {
            "round": 1,
            "proposer_address": "EFGH5678...",
            "proposed": false
          },
          {
            "round": 2,
            "proposer_address": "IJKL9012...",
            "proposed": true
          }
        ]
      }
    },
    "canonical": true
  }
}
```

### Veri Analizi / Data Analysis

Round proposer bilgileri ile:
- Hangi validatörün hangi round'da proposer olduğunu görebilirsiniz
- Proposer'ların block propose etme başarı oranlarını hesaplayabilirsiniz
- Hangi proposer'ların miss ettiğini (proposed=false) tespit edebilirsiniz
- Network performans metriklerini çıkarabilirsiniz

## Örnek Senaryo / Example Scenario

```
Height 100 için block oluşturma süreci:

Round 0: Validator A proposer seçildi
  - Validator A block propose edemedi (network problemi)
  - Kaydedilen: {round: 0, proposer_address: A, proposed: false}
  
Round 1: Validator B proposer seçildi  
  - Validator B block propose edemedi (timeout)
  - Kaydedilen: {round: 1, proposer_address: B, proposed: false}

Round 2: Validator C proposer seçildi
  - Validator C başarıyla block propose etti
  - Block commit edildi
  - Kaydedilen: {round: 2, proposer_address: C, proposed: true}

Sonuç: Height 100'ün commit bilgisinde 3 round proposer kaydı var
```

## Teknik Detaylar / Technical Details

### Thread Safety
- `roundProposers` map'i `roundProposersMtx` mutex ile korunuyor
- Read/Write operasyonlarında uygun lock mekanizması kullanılıyor

### Memory Management
- Her yeni height başladığında roundProposers map'i sıfırlanıyor
- Sadece current height'ın round bilgileri bellekte tutuluyor

### Sync Compatibility
- Block sync sırasında ExtendedCommit ile birlikte roundProposers bilgisi de sync ediliyor
- State sync sonrası da veriler tutarlı

## Protobuf Code Generation

Protobuf tanımlarını güncelledikten sonra Go kodlarını generate etmek için:

```bash
make proto-gen
```

veya

```bash
cd proto
buf generate
```

## Testing

Değişiklikleri test etmek için:

```bash
# Unit testleri çalıştır
make test

# Belirli bir package'ı test et
go test ./types/...
go test ./consensus/...

# Integration testleri
make test-integration
```

## Notlar / Notes

- Bu özellik upgrade sonrası aktif olacak
- Eski blocklar roundProposers bilgisi içermeyecek (boş array)
- Yeni blocklar otomatik olarak bu bilgiyi içerecek
- Blockchain state'i veya consensus kuralları değişmedi
- Sadece ek bilgi kaydediliyor

## İleride Yapılabilecekler / Future Enhancements

1. RPC endpoint'leri ile round proposer istatistikleri
2. Prometheus metrics ile proposer performance tracking
3. Validator performance dashboard'u
4. Automated alerting for frequently missing proposers

---

**Geliştirici:** AI Assistant (Claude Sonnet 4.5)
**Tarih:** October 23, 2025
**Versiyon:** celestia-core v0.39.10+ (custom feature)

